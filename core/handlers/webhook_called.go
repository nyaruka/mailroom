package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/hooks"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	models.RegisterEventHandler(events.TypeWebhookCalled, handleWebhookCalled)
}

// handleWebhookCalled is called for each webhook call in a scene
func handleWebhookCalled(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scene *models.Scene, e flows.Event) error {
	event := e.(*events.WebhookCalledEvent)

	slog.Debug("webhook called", "contact", scene.ContactUUID(), "session", scene.SessionID(), "url", event.URL, "status", event.Status, "elapsed_ms", event.ElapsedMS)

	// if this was a resthook and the status was 410, that means we should remove it
	if event.Status == flows.CallStatusSubscriberGone {
		unsub := &models.ResthookUnsubscribe{
			OrgID: oa.OrgID(),
			Slug:  event.Resthook,
			URL:   event.URL,
		}

		scene.AppendToEventPreCommitHook(hooks.UnsubscribeResthookHook, unsub)
	}

	flow, nodeUUID := scene.Session().LocateEvent(e)

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
		scene.AppendToEventPreCommitHook(hooks.InsertHTTPLogsHook, httpLog)
	}

	// pass node and response time to the hook that monitors webhook health
	scene.AppendToEventPreCommitHook(hooks.MonitorWebhooks, &hooks.WebhookCall{NodeUUID: nodeUUID, Event: event})

	return nil
}
