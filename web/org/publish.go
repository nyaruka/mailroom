package org

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/org/publish", web.JSONPayload(handlePublish))
}

// Publishes an already-rendered event to a workspace's shared realtime socket.
//
//	{"org_id": 1, "event": {"type": "asset_changed", "asset": {"type": "flow", "uuid": "...", "name": "Registration"}}}
type publishRequest struct {
	OrgID models.OrgID    `json:"org_id" validate:"required"`
	Event json.RawMessage `json:"event"  validate:"required"`
}

func handlePublish(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	event := bytes.TrimSpace(r.Event)
	if len(event) == 0 || event[0] != '{' {
		return fmt.Errorf("event must be a JSON object"), http.StatusBadRequest, nil
	}

	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets for org #%d: %w", r.OrgID, err)
	}
	if err := models.PublishOrgEvent(ctx, rt, oa.Org().UUID(), event); err != nil {
		return nil, 0, fmt.Errorf("error publishing event for org #%d: %w", r.OrgID, err)
	}

	return map[string]any{}, http.StatusOK, nil
}
