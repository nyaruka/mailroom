package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/flow/clone", web.JSONPayload(handleClone))
}

// Clones a flow, replacing all UUIDs with either the given mapping or new random UUIDs.
//
//	{
//	  "dependency_mapping": {
//	    "4ee4189e-0c06-4b00-b54f-5621329de947": "db31d23f-65b8-4518-b0f6-45638bfbbbf2",
//	    "723e62d8-a544-448f-8590-1dfd0fccfcd4": "f1fd861c-9e75-4376-a829-dcf76db6e721"
//	  },
//	  "flow": { "uuid": "468621a8-32e6-4cd2-afc1-04416f7151f0", "nodes": [...]}
//	}
type cloneRequest struct {
	DependencyMapping map[uuids.UUID]uuids.UUID `json:"dependency_mapping"`
	Flow              json.RawMessage           `json:"flow" validate:"required"`
}

func handleClone(ctx context.Context, rt *runtime.Runtime, r *cloneRequest) (any, int, error) {
	// try to clone the flow definition
	cloneJSON, err := goflow.CloneDefinition(r.Flow, r.DependencyMapping)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to read flow: %w", err)
	}

	// read flow to check that cloning produced something valid
	_, err = goflow.ReadFlow(rt.Config, cloneJSON)
	if err != nil {
		return nil, 0, fmt.Errorf("unable to clone flow: %w", err)
	}

	return json.RawMessage(cloneJSON), http.StatusOK, nil
}
