package task

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodGet, "/task/batches", web.MarshaledResponse(handleBatches))
}

const maxBatchSets = 100

// Returns the sets of batch tasks which haven't finished, across all orgs, least recently active first. Each set is
// the batches split out from a single flow start, broadcast, contact import or group population.
//
//	{
//	  "sets": [
//	    {
//	      "owner_uuid": "0199bcb8-4e12-7e7f-a4d2-fabbf9d69dbb",
//	      "info": {"type": "start_flow_batch", "org_id": 1, "label": "Favorites", "total": 40, "queued_on": "2025-06-12T14:12:00Z"},
//	      "started": true,
//	      "completed": 12,
//	      "last_on": "2025-06-12T15:30:45Z"
//	    }
//	  ]
//	}
func handleBatches(ctx context.Context, rt *runtime.Runtime, r *http.Request) (any, int, error) {
	sets, err := tasks.GetBatchTasks(ctx, rt.VK, maxBatchSets)
	if err != nil {
		return nil, 0, fmt.Errorf("error getting batch task sets: %w", err)
	}

	return map[string]any{"sets": sets}, http.StatusOK, nil
}
