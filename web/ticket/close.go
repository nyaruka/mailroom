package ticket

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/ticket/close", web.JSONPayload(handleClose))
}

type closeRequest struct {
	bulkTicketRequest
}

// Closes any open tickets with the given ids.
//
//	{
//	  "org_id": 123,
//	  "user_id": 234,
//	  "ticket_ids": [1234, 2345]
//	}
func handleClose(ctx context.Context, rt *runtime.Runtime, r *closeRequest) (any, int, error) {
	// grab our org assets
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to load org assets: %w", err)
	}

	mod := modifiers.NewTicketClose(r.TicketUUIDs)

	scenes, err := createTicketScenes(ctx, rt, oa, r.TicketUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating scenes for tickets: %w", err)
	}

	changed := make([]flows.TicketUUID, 0, len(scenes))
	tasks := make(map[models.ContactID][]realtime.Task, len(scenes))

	for _, scene := range scenes {
		evts, err := scene.ApplyModifier(ctx, rt, oa, mod, r.UserID)
		if err != nil {
			return nil, 0, fmt.Errorf("error applying ticket modifier to scene: %w", err)
		}

		for _, e := range evts {
			switch typed := e.(type) {
			case *events.TicketClosed:
				changed = append(changed, typed.TicketUUID)
				tasks[scene.ContactID()] = append(tasks[scene.ContactID()], ctasks.NewTicketClosed(typed))
			}
		}
	}

	if err := runner.BulkCommit(ctx, rt, oa, scenes); err != nil {
		return nil, 0, fmt.Errorf("error committing scenes for tickets: %w", err)
	}

	for contactID, contactTasks := range tasks {
		for _, task := range contactTasks {
			if err := realtime.QueueTask(ctx, rt, oa.OrgID(), contactID, task); err != nil {
				return nil, 0, fmt.Errorf("error queueing ticket closed task: %w", err)
			}
		}
	}

	return newBulkResponse(changed), http.StatusOK, nil
}
