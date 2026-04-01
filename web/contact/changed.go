package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/changed", web.JSONPayload(handleChanged))
}

// Queues a contact changed task for the given contact.
//
//	{
//	  "org_id": 1,
//	  "contact_id": 12345,
//	  "new_urn": {"value": "whatsapp:250788123123", "action": "append"}
//	}
type changedRequest struct {
	OrgID     models.OrgID       `json:"org_id"      validate:"required"`
	ContactID models.ContactID   `json:"contact_id"  validate:"required"`
	NewURN    *ctasks.NewURNSpec `json:"new_urn"     validate:"required"`
}

func handleChanged(ctx context.Context, rt *runtime.Runtime, r *changedRequest) (any, int, error) {
	err := tasks.QueueContact(ctx, rt, r.OrgID, r.ContactID, &ctasks.ContactChanged{
		NewURN: r.NewURN,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("error queuing contact changed task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
