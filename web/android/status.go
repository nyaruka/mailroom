package android

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/status", web.JSONPayload(handleStatus))
}

// Applies message status changes reported by an Android relayer during a sync.
//
//	{
//	  "org_id": 1,
//	  "commands": [
//	    {"msg_id": 12345, "cmd": "mt_sent", "ts": "2021-01-01T12:00:00Z"},
//	    {"msg_id": 12346, "cmd": "mt_dlvd", "ts": "2021-01-01T12:00:05Z"}
//	  ]
//	}
//
// The response says whether each command was handled, in the order they were given, so that the caller can tell the
// relayer which ones to stop resending:
//
//	{
//	  "handled": [true, false]
//	}
//
// Commands are identified by message id rather than UUID like every other message endpoint because a relayer only
// ever knows the ids it was given in its sync payload, and resolving those to UUIDs first would mean an extra query
// that still has to preserve which ids didn't resolve.
type statusRequest struct {
	OrgID    models.OrgID     `json:"org_id"   validate:"required"`
	Commands []*statusCommand `json:"commands" validate:"required,dive"`
}

type statusCommand struct {
	MsgID models.MsgID `json:"msg_id" validate:"required"`
	Cmd   string       `json:"cmd"    validate:"required"`
	Ts    time.Time    `json:"ts"     validate:"required"`
}

// the message status commands a relayer can send, and the status each one puts the message into
var statusCommands = map[string]models.MsgStatus{
	"mt_error": models.MsgStatusErrored,
	"mt_fail":  models.MsgStatusFailed,
	"mt_sent":  models.MsgStatusSent,
	"mt_dlvd":  models.MsgStatusDelivered,
}

func handleStatus(ctx context.Context, rt *runtime.Runtime, r *statusRequest) (any, int, error) {
	msgIDs := make([]models.MsgID, len(r.Commands))
	for i, c := range r.Commands {
		msgIDs[i] = c.MsgID
	}

	msgs, err := models.GetMessagesByID(ctx, rt.DB, r.OrgID, msgIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to update: %w", err)
	}

	msgsByID := make(map[models.MsgID]*models.Msg, len(msgs))
	for _, m := range msgs {
		msgsByID[m.ID()] = m
	}

	// whether each command was applied, so that the caller can tell the relayer which ones to stop resending
	handled := make([]bool, len(r.Commands))

	// a sync can report more than one change for the same message, e.g. sent and then delivered, and they all have
	// to be folded into a single update because they're applied in one statement
	updates := make([]*models.AndroidStatusUpdate, 0, len(r.Commands))
	updatesByID := make(map[models.MsgID]*models.AndroidStatusUpdate, len(r.Commands))

	for i, c := range r.Commands {
		m := msgsByID[c.MsgID]
		if m == nil {
			continue
		}

		// incoming messages have no status for a relayer to report but it shouldn't keep resending the command
		if m.Direction() == models.DirectionIn {
			handled[i] = true
			continue
		}

		status, exists := statusCommands[c.Cmd]
		if !exists {
			continue
		}

		u := updatesByID[c.MsgID]
		if u == nil {
			u = &models.AndroidStatusUpdate{MsgID: c.MsgID}
			updates = append(updates, u)
			updatesByID[c.MsgID] = u
		}

		u.Status = status

		switch c.Cmd {
		case "mt_sent":
			// this is the definitive report of when the message left the phone
			u.SentOn = &c.Ts
			u.OverwriteSentOn = true
		case "mt_dlvd":
			// delivery only tells us when the message arrived, so it stands in for sent_on only if no sent report
			// ever arrived - either earlier in this sync or on the message already
			if u.SentOn == nil {
				u.SentOn = &c.Ts
			}
		}

		handled[i] = true
	}

	tags, err := models.UpdateAndroidMessageStatuses(ctx, rt.DB, r.OrgID, updates)
	if err != nil {
		return nil, 0, fmt.Errorf("error updating message statuses: %w", err)
	}

	// record each change in the contact's history so that clients rendering the message see its new status
	for _, tag := range tags {
		if _, err := rt.Dynamo.History.Queue(tag); err != nil {
			return nil, 0, fmt.Errorf("error queuing status tag to writer: %w", err)
		}
	}

	return map[string]any{"handled": handled}, http.StatusOK, nil
}
