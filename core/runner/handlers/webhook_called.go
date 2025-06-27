package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeWebhookCalled, handleWebhookCalled)
}

// handleWebhookCalled is called for each webhook call in a scene
func handleWebhookCalled(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.WebhookCalled)

	slog.Debug("webhook called", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "url", event.URL, "status", event.Status, "elapsed_ms", event.ElapsedMS)

	// if this was a resthook and the status was 410, that means we should remove it
	if event.Status == flows.CallStatusSubscriberGone {
		unsub := &models.ResthookUnsubscribe{
			OrgID: oa.OrgID(),
			Slug:  event.Resthook,
			URL:   event.URL,
		}

		scene.AttachPreCommitHook(hooks.UnsubscribeResthook, unsub)
	}

	flow, nodeUUID := scene.LocateEvent(e)

	// create an HTTP log
	if flow != nil {
		httpLog := models.NewWebhookCalledLog(
			oa.OrgID(),
			flow.ID(),
			event.URL, event.StatusCode, event.Request, event.Response,
			event.Status != flows.CallStatusSuccess,
			time.Millisecond*time.Duration(event.ElapsedMS),
			event.Retries,
			event.CreatedOn(),
		)
		scene.AttachPreCommitHook(hooks.InsertHTTPLogs, httpLog)
	}

	rt.Stats.RecordWebhookCall(time.Duration(event.ElapsedMS) * time.Millisecond)

	// pass node and response time to the hook that monitors webhook health
	scene.AttachPreCommitHook(hooks.MonitorWebhooks, &hooks.WebhookCall{NodeUUID: nodeUUID, Event: event})

	return nil
}
