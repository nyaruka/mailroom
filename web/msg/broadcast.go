package msg

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/msg/broadcast", web.JSONPayload(handleBroadcast))
}

// Request to send a broadcast.
//
//	{
//	  "org_id": 1,
//	  "user_id": 56,
//	  "translations": {"eng": {"text": "Hello @contact"}, "spa": {"text": "Hola @contact"}},
//	  "base_language": "eng",
//	  "optin_id": 456,
//	  "group_ids": [101, 102],
//	  "contact_ids": [4646],
//	  "urns": [4646],
//	  "schedule": {
//	    "start": "2024-06-20T09:04:30Z",
//	    "repeat_period": "W",
//	    "repeat_days_of_week": "MF"
//	  }
//	}
type broadcastRequest struct {
	OrgID             models.OrgID                `json:"org_id"        validate:"required"`
	UserID            models.UserID               `json:"user_id"       validate:"required"`
	Translations      flows.BroadcastTranslations `json:"translations"  validate:"required"`
	BaseLanguage      i18n.Language               `json:"base_language" validate:"required"`
	OptInID           models.OptInID              `json:"optin_id"`
	TemplateID        models.TemplateID           `json:"template_id"`
	TemplateVariables []string                    `json:"template_variables"`
	GroupIDs          []models.GroupID            `json:"group_ids"`
	ContactIDs        []models.ContactID          `json:"contact_ids"`
	URNs              []urns.URN                  `json:"urns"`
	Query             string                      `json:"query"`
	NodeUUID          flows.NodeUUID              `json:"node_uuid"`
	Exclude           models.Exclusions           `json:"exclude"`
	Schedule          *struct {
		Start            time.Time           `json:"start"`
		RepeatPeriod     models.RepeatPeriod `json:"repeat_period"`
		RepeatDaysOfWeek string              `json:"repeat_days_of_week"`
	} `json:"schedule"`
}

// handles a request to create the given broadcast
func handleBroadcast(ctx context.Context, rt *runtime.Runtime, r *broadcastRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	if len(r.ContactIDs) == 0 && len(r.GroupIDs) == 0 && len(r.URNs) == 0 && r.Query == "" && r.NodeUUID == "" {
		return errors.New("can't create broadcast with no recipients"), http.StatusBadRequest, nil
	}

	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("error beginning transaction: %w", err)
	}

	bcast := &models.Broadcast{
		UUID:              flows.NewBroadcastUUID(),
		OrgID:             r.OrgID,
		Status:            models.BroadcastStatusPending,
		Translations:      r.Translations,
		BaseLanguage:      r.BaseLanguage,
		Expressions:       true,
		OptInID:           r.OptInID,
		TemplateID:        r.TemplateID,
		TemplateVariables: r.TemplateVariables,
		GroupIDs:          r.GroupIDs,
		ContactIDs:        r.ContactIDs,
		URNs:              r.URNs,
		Query:             r.Query,
		NodeUUID:          r.NodeUUID,
		Exclusions:        r.Exclude,
		CreatedByID:       r.UserID,
	}

	if r.Schedule != nil {
		sched, err := models.NewSchedule(oa, r.Schedule.Start, r.Schedule.RepeatPeriod, r.Schedule.RepeatDaysOfWeek)
		if err != nil {
			return fmt.Errorf("error creating schedule: %w", err), http.StatusBadRequest, nil
		}

		if err := sched.Insert(ctx, tx); err != nil {
			return nil, 0, fmt.Errorf("error inserting schedule: %w", err)
		}

		bcast.ScheduleID = sched.ID
	}

	if err := models.InsertBroadcast(ctx, tx, bcast); err != nil {
		return nil, 0, fmt.Errorf("error inserting broadcast: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, 0, fmt.Errorf("error committing transaction: %w", err)
	}

	// if broadcast doesn't have a schedule, queue it up for immediate sending
	if r.Schedule == nil {
		task := &tasks.SendBroadcast{Broadcast: bcast}

		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, bcast.OrgID, task, true); err != nil {
			return nil, 0, fmt.Errorf("error queuing send broadcast task: %w", err)
		}
	}

	return map[string]any{"id": bcast.ID}, http.StatusOK, nil
}
