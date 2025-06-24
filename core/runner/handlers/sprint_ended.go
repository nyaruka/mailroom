package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

const (
	// SessionExpires is the default *overall* expiration time for a session
	SessionExpires = 30 * 24 * time.Hour
)

func init() {
	runner.RegisterEventHandler(runner.TypeSprintEnded, handleSprintEnded)
}

func handleSprintEnded(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event) error {
	event := e.(*runner.SprintEndedEvent)

	slog.Debug("sprint ended", "contact", scene.ContactUUID(), "session", scene.SessionUUID())

	sessionIsWaiting := scene.Session.Status() == flows.SessionStatusWaiting
	currentFlowChanged := false

	// get flow that contact is now waiting in ()
	currentFlowID := models.NilFlowID
	for _, run := range scene.Session.Runs() {
		if run.Status() == flows.RunStatusWaiting {
			currentFlowID = run.Flow().Asset().(*models.Flow).ID()
			break
		}
	}

	// if we're in a flow type that can wait then contact current flow has potentially changed
	if scene.Session.Type() != flows.FlowTypeMessagingBackground {
		var waitingSessionUUID flows.SessionUUID
		if sessionIsWaiting {
			waitingSessionUUID = scene.Session.UUID()
		}

		currentFlowChanged = event.Contact.CurrentFlowID() != currentFlowID

		if event.Contact.CurrentSessionUUID() != waitingSessionUUID || currentFlowChanged {
			scene.AttachPreCommitHook(hooks.UpdateContactSession, hooks.CurrentSessionUpdate{
				ID:                 scene.ContactID(),
				CurrentSessionUUID: null.String(waitingSessionUUID),
				CurrentFlowID:      currentFlowID,
			})
		}
	}

	// if current flow has changed then we need to update modified_on, but also if this is a new session
	// then flow history may have changed too in a way that won't be captured by a flow_entered event
	if currentFlowChanged || !event.Resumed {
		scene.AttachPreCommitHook(hooks.UpdateContactModifiedOn, event)
	}

	if scene.Call != nil {
		if scene.Session.Status() != flows.SessionStatusWaiting {
			scene.AttachPreCommitHook(hooks.UpdateCallStatus, models.CallStatusCompleted)
		} else if scene.Sprint.IsInitial() {
			scene.AttachPreCommitHook(hooks.UpdateCallStatus, models.CallStatusInProgress)
		}
	}

	if scene.Session.Status() != flows.SessionStatusFailed {
		newFires, timeout := calculateFires(oa, scene.ContactID(), scene.Session, scene.Sprint, scene.Sprint.IsInitial())

		scene.WaitTimeout = timeout // used by post commit hooks

		delFires := hooks.DeleteFiresNone
		if scene.Sprint.IsInitial() {
			// we've started a new session
			if sessionIsWaiting {
				// and reached a wait
				delFires = hooks.DeleteFiresAll // TODO are we over-deleting fires for new waiting sessions?
			}
		} else {
			// we've resumed an existing session
			if sessionIsWaiting {
				// and hit another wait
				delFires = hooks.DeleteFiresWaits
			} else {
				// and completed
				delFires = hooks.DeleteFiresAll
			}
		}

		scene.AttachPreCommitHook(hooks.InsertContactFires, hooks.FiresSet{Create: newFires, Delete: delFires})
		scene.AttachPreCommitHook(hooks.InsertFlowStats, event)
	}
	return nil
}

// calculates the fires needed for the given session - returns timeout separately if this session will queue messages to courier
func calculateFires(oa *models.OrgAssets, contactID models.ContactID, session flows.Session, sprint flows.Sprint, initial bool) ([]*models.ContactFire, time.Duration) {
	waitExpiresOn, waitTimeout, queuesToCourier := getWaitProperties(oa, sprint.Events())
	var waitTimeoutOn *time.Time
	var timeout time.Duration

	if waitTimeout != 0 {
		if queuesToCourier {
			timeout = waitTimeout
		} else {
			ton := dates.Now().Add(waitTimeout)
			waitTimeoutOn = &ton
		}
	}

	fires := make([]*models.ContactFire, 0, 3)

	if waitTimeoutOn != nil {
		fires = append(fires, models.NewFireForSession(oa.OrgID(), contactID, session.UUID(), sprint.UUID(), models.ContactFireTypeWaitTimeout, *waitTimeoutOn))
	}
	if waitExpiresOn != nil {
		fires = append(fires, models.NewFireForSession(oa.OrgID(), contactID, session.UUID(), sprint.UUID(), models.ContactFireTypeWaitExpiration, *waitExpiresOn))
	}
	if initial && session.Status() == flows.SessionStatusWaiting {
		// session expiration time is the creation time + 30 days + random time between 0 and 24 hours
		sessionExpiresOn := session.CreatedOn().Add(SessionExpires).Add(time.Duration(random.IntN(86_400)) * time.Second)

		fires = append(fires, models.NewFireForSession(oa.OrgID(), contactID, session.UUID(), "", models.ContactFireTypeSessionExpiration, sessionExpiresOn))
	}

	return fires, timeout
}

// looks thru sprint events to figure out if we have a wait on this session and if so what is its expiration and timeout
func getWaitProperties(oa *models.OrgAssets, evts []flows.Event) (*time.Time, time.Duration, bool) {
	var expiresOn *time.Time
	var timeout time.Duration
	var queuesToCourier bool

	for _, e := range evts {
		switch typed := e.(type) {
		case *events.MsgWaitEvent:
			expiresOn = &typed.ExpiresOn

			if typed.TimeoutSeconds != nil {
				timeout = time.Duration(*typed.TimeoutSeconds) * time.Second
			}
		case *events.DialWaitEvent:
			expiresOn = &typed.ExpiresOn
		case *events.MsgCreatedEvent:
			if typed.Msg.Channel() != nil {
				channel := oa.ChannelByUUID(typed.Msg.Channel().UUID)
				if channel != nil && !channel.IsAndroid() {
					queuesToCourier = true
				}
			}
		}
	}

	return expiresOn, timeout, queuesToCourier
}
