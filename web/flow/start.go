package flow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/flow/start", web.JSONPayload(handleStart))
}

// Request to start a flow.
//
//	{
//	  "org_id": 1,
//	  "user_id": 56,
//	  "type": "M",
//	  "flow_id": 123,
//	  "group_ids": [101, 102],
//	  "contact_ids": [4646],
//	  "urns": [4646]
//	}
type startRequest struct {
	OrgID      models.OrgID       `json:"org_id"       validate:"required"`
	UserID     models.UserID      `json:"user_id"      validate:"required"`
	Type       models.StartType   `json:"type"         validate:"required"`
	FlowID     models.FlowID      `json:"flow_id"      validate:"required"`
	GroupIDs   []models.GroupID   `json:"group_ids"`
	ContactIDs []models.ContactID `json:"contact_ids"`
	URNs       []urns.URN         `json:"urns"`
	Query      string             `json:"query"`
	Exclude    models.Exclusions  `json:"exclude"`
	Params     json.RawMessage    `json:"params"`
}

func handleStart(ctx context.Context, rt *runtime.Runtime, r *startRequest) (any, int, error) {
	if len(r.ContactIDs) == 0 && len(r.GroupIDs) == 0 && len(r.URNs) == 0 && r.Query == "" {
		return errors.New("can't create flow start with no recipients"), http.StatusBadRequest, nil
	}

	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error beginning transaction: %w", err)
	}

	start := &models.FlowStart{
		UUID:        uuids.NewV4(),
		OrgID:       r.OrgID,
		Status:      models.StartStatusPending,
		StartType:   r.Type,
		FlowID:      r.FlowID,
		GroupIDs:    r.GroupIDs,
		ContactIDs:  r.ContactIDs,
		URNs:        r.URNs,
		Query:       r.Query,
		Exclusions:  r.Exclude,
		Params:      r.Params,
		CreatedByID: r.UserID,
	}

	if err := models.InsertFlowStart(ctx, tx, start); err != nil {
		return nil, 0, fmt.Errorf("error inserting flow start: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("error committing transaction: %w", err)
	}

	// queue it up for actual starting
	task := &tasks.StartFlow{FlowStart: start}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, r.OrgID, task, false); err != nil {
		return nil, 0, fmt.Errorf("error queuing start flow task: %w", err)
	}

	return map[string]any{"id": start.ID}, http.StatusOK, nil
}
