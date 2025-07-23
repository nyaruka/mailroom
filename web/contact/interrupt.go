package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/interrupts"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/contact/interrupt", web.RequireAuthToken(web.JSONPayload(handleInterrupt)))
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

// handles a request to interrupt a contact
func handleInterrupt(ctx context.Context, rt *runtime.Runtime, r *interruptRequest) (any, int, error) {
	var count int
	var err error

	if len(r.ContactIDs) == 1 {
		count, err = models.InterruptSessionsForContacts(ctx, rt.DB, r.ContactIDs)
		if err != nil {
			return nil, 0, fmt.Errorf("unable to interrupt contact: %w", err)
		}
	} else if len(r.ContactIDs) > 0 {
		task := &interrupts.InterruptSessionsTask{ContactIDs: r.ContactIDs}
		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, true); err != nil {
			return nil, 0, fmt.Errorf("error queuing interrupt flow task: %w", err)
		}
	}

	return map[string]any{"sessions": count}, http.StatusOK, nil
}
