package contact

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/contact/deindex", web.JSONPayload(handleDeindex))
}

// Requests de-indexing of the given contacts from Elastic indexes.
//
//	{
//	  "org_id": 1,
//	  "contact_uuids": ["548f43fb-f32a-491f-abb7-0c29a453a06e"]
//	}
type deindexRequest struct {
	OrgID        models.OrgID        `json:"org_id"        validate:"required"`
	ContactUUIDs []flows.ContactUUID `json:"contact_uuids" validate:"required"`
}

func handleDeindex(ctx context.Context, rt *runtime.Runtime, r *deindexRequest) (any, int, error) {
	task := &tasks.DeindexContacts{
		ContactUUIDs: r.ContactUUIDs,
	}

	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, false); err != nil {
		return nil, 0, fmt.Errorf("error queuing deindex contacts task: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
