package system

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodGet, "/system/queues", web.JSONPayload(handleQueues))
}

// Requests information about the task queues.
//
//	{}
type queuesRequest struct {
}

// handles a request to get queue information
func handleQueues(ctx context.Context, rt *runtime.Runtime, r *queuesRequest) (any, int, error) {
	vc := rt.VK.Get()
	defer vc.Close()

	resp := map[string]any{}

	for _, queue := range []queues.Fair{rt.Queues.Realtime, rt.Queues.Batch, rt.Queues.Throttled} {
		dump, err := queue.Dump(ctx, vc)
		if err != nil {
			return nil, 0, fmt.Errorf("error dumping queue %s: %w", queue, err)
		}
		resp[fmt.Sprint(queue)] = json.RawMessage(dump)
	}

	return resp, http.StatusOK, nil
}
