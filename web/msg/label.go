package msg

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/label", web.JSONPayload(handleLabel))
}

// Adds or removes a label on the given incoming messages. Messages which already have or don't have the label are
// left alone, and only messages whose labelling actually changed have their modified_on updated.
//
//	{
//	  "org_id": 1,
//	  "label_uuid": "a6338cdc-7938-4437-8b05-2d5d785e3a08",
//	  "msg_uuids": ["0199bada-2b39-7cac-9714-827df9ec6b91", "0199bb09-f0e9-7489-a58e-69304a7941a0"],
//	  "add": true
//	}
type labelRequest struct {
	OrgID     models.OrgID       `json:"org_id"     validate:"required"`
	LabelUUID assets.LabelUUID   `json:"label_uuid" validate:"required"`
	MsgUUIDs  []events.EventUUID `json:"msg_uuids"  validate:"required"`
	Add       bool               `json:"add"`
}

func handleLabel(ctx context.Context, rt *runtime.Runtime, r *labelRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	label := oa.LabelByUUID(r.LabelUUID)
	if label == nil {
		return errors.New("no such label"), http.StatusBadRequest, nil
	}

	msgs, err := models.GetMessagesByUUID(ctx, rt.DB, r.OrgID, models.DirectionIn, r.MsgUUIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading messages to label: %w", err)
	}

	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var changed []models.MsgID

	if r.Add {
		adds := make([]*models.MsgLabelAdd, len(msgs))
		for i, m := range msgs {
			adds[i] = &models.MsgLabelAdd{MsgUUID: m.UUID(), LabelID: label.ID()}
		}
		changed, err = models.AddMsgLabels(ctx, tx, adds)
		if err != nil {
			return nil, 0, fmt.Errorf("error adding label to messages: %w", err)
		}
	} else {
		ids := make([]models.MsgID, len(msgs))
		for i, m := range msgs {
			ids[i] = m.ID()
		}
		changed, err = models.RemoveMsgLabels(ctx, tx, label.ID(), ids)
		if err != nil {
			return nil, 0, fmt.Errorf("error removing label from messages: %w", err)
		}
	}

	if err := models.UpdateMessagesModifiedOn(ctx, tx, changed); err != nil {
		return nil, 0, fmt.Errorf("error updating modified_on for labelled messages: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("error committing transaction: %w", err)
	}

	return map[string]any{}, http.StatusOK, nil
}
