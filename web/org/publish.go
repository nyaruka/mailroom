package org

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/org/publish", web.JSONPayload(handlePublish))
}

// Publishes an already-rendered event to a workspace's shared realtime socket. Used by the platform's Django side for
// the workspace state it owns (e.g. flow and group renames), so that both producers of workspace events share the
// socket addressing and subscriber-presence check. Beyond requiring an object with a type, the event is forwarded to
// subscribers verbatim and unvalidated - the rendering is the caller's, so mailroom needs no knowledge of the event
// types, and equally can't tell a client-actionable event from one no client will act on.
//
//	{"org_id": 1, "event": {"type": "asset_changed", "asset": {"type": "flow", "uuid": "...", "name": "Registration"}}}
type publishRequest struct {
	OrgID models.OrgID    `json:"org_id" validate:"required"`
	Event json.RawMessage `json:"event"  validate:"required"`
}

func handlePublish(ctx context.Context, rt *runtime.Runtime, r *publishRequest) (any, int, error) {
	// `validate:"required"` doesn't reject `null` or a non-object payload, but the realtime protocol assumes an
	// object, so guard here rather than forwarding e.g. literal `null` to a subscriber's socket. PublishOrgEvent
	// rejects the same thing for in-process callers - we check first so a caller bug is a 400 and not a 500.
	if len(r.Event) == 0 || r.Event[0] != '{' {
		return errors.New("event must be a JSON object"), http.StatusBadRequest, nil
	}

	// clients dispatch on the event type, so an event without one can only be a caller bug - and one that would
	// otherwise be silent, since we forward payloads we don't ourselves understand
	var typed struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(r.Event, &typed); err != nil || typed.Type == "" {
		return errors.New("event must have a type"), http.StatusBadRequest, nil
	}

	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets for org #%d: %w", r.OrgID, err)
	}

	if err := models.PublishOrgEvent(ctx, rt, oa, r.Event); err != nil {
		return nil, 0, fmt.Errorf("error publishing event for org #%d: %w", r.OrgID, err)
	}

	return map[string]any{}, http.StatusOK, nil
}
