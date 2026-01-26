package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/interrupt", web.JSONPayload(handleInterrupt))
}

// Request that contacts are interrupted. If passed a single contact, their sessions are interrupted immediately. If
// passed multiple contacts, a task is queued to interrupt their sessions.
//
//	{
//	  "org_id": 1,
//	  "user_id": 3,
//	  "contact_ids": [234, 345]
//	}
type interruptRequest struct {
	OrgID      models.OrgID       `json:"org_id"      validate:"required"`
	UserID     models.UserID      `json:"user_id"     validate:"required"`
	ContactIDs []models.ContactID `json:"contact_ids" validate:"required"`
}

// Response for contact interruption. Will return the events generated. Contacts that we couldn't
// get a lock for are returned in skipped.
//
//	{
//	  "events": {
//	    "559d4cf7-8ed3-43db-9bbb-2be85345f87e": [...]
//	    ...
//	  },
//	  "skipped": [1006, 1007]
//	}
type interruptResponse struct {
	Events  map[flows.ContactUUID][]flows.Event `json:"events"`
	Skipped []models.ContactID                  `json:"skipped"`
}

// handles a request to interrupt a contact
func handleInterrupt(ctx context.Context, rt *runtime.Runtime, r *interruptRequest) (any, int, error) {
	resp := &interruptResponse{}

	if len(r.ContactIDs) == 1 {
		oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
		if err != nil {
			return nil, 0, fmt.Errorf("error loading org assets: %w", err)
		}

		eventsByContact, skipped, err := runner.InterruptWithLock(ctx, rt, oa, r.ContactIDs, nil, flows.SessionStatusInterrupted)
		if err != nil {
			return nil, 0, fmt.Errorf("error interrupting contact: %w", err)
		}

		resp.Events = make(map[flows.ContactUUID][]flows.Event, len(eventsByContact))
		for contact, events := range eventsByContact {
			resp.Events[contact.UUID()] = events
		}
		resp.Skipped = skipped

	} else if len(r.ContactIDs) > 0 {
		task := &tasks.InterruptSessions{ContactIDs: r.ContactIDs}
		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
			return nil, 0, fmt.Errorf("error queuing interrupt flow task: %w", err)
		}

		resp.Events = map[flows.ContactUUID][]flows.Event{}
		resp.Skipped = []models.ContactID{}
	}

	return resp, http.StatusOK, nil
}
