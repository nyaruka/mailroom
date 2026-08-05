package ctasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// Task is the interface for all contact tasks - tasks which operate on a single contact in real time
type Task interface {
	Type() string
	Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, mc *models.Contact) error
}

var registeredTypes = map[string]func() Task{}

func RegisterType(name string, initFunc func() Task) {
	registeredTypes[name] = initFunc
}

func ReadTask(type_ string, data []byte) (Task, error) {
	fn := registeredTypes[type_]
	if fn == nil {
		return nil, fmt.Errorf("unknown task type: %s", type_)
	}

	t := fn()
	return t, utils.UnmarshalAndValidate(data, t)
}

// Payload wrapper for encoding a contact task
type Payload struct {
	Type       string          `json:"type"`
	Task       json.RawMessage `json:"task"`
	QueuedOn   time.Time       `json:"queued_on"`
	ErrorCount int             `json:"error_count,omitempty"`
}

// Queue adds a contact task to a contact's queue
func Queue(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactID models.ContactID, task Task, front bool, errorCount int) error {
	vc := rt.VK.Get()
	defer vc.Close()

	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("error marshalling contact task: %w", err)
	}

	payload := &Payload{Type: task.Type(), Task: taskJSON, QueuedOn: dates.Now(), ErrorCount: errorCount}
	payloadJSON := jsonx.MustMarshal(payload)

	// first push the event on our contact queue
	contactQ := fmt.Sprintf("c:%d:%d", orgID, contactID)
	if front {
		_, err = valkey.Int64(valkey.DoContext(vc, ctx, "LPUSH", contactQ, string(payloadJSON)))

	} else {
		_, err = valkey.Int64(valkey.DoContext(vc, ctx, "RPUSH", contactQ, string(payloadJSON)))
	}
	if err != nil {
		return fmt.Errorf("error queuing contact task: %w", err)
	}

	return nil
}

// NewURNSpec describes a new URN to add to a contact
type NewURNSpec struct {
	Value  urns.URN `json:"value" validate:"required"`
	Action string   `json:"action" validate:"required,eq=append"`
}

// Apply appends the new URN to the contact, recording channel affinity for the given channel if set.
func (s *NewURNSpec) Apply(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, channel *models.Channel) error {
	// as WhatsApp identity transitions from phone numbers to BSUIDs, a BSUID being appended here may already be owned
	// by a duplicate contact created from messages received with only that BSUID - if that contact has no other URNs
	// we reassign the URN to this contact, which lets the append below proceed and set priority and channel affinity
	if urns.IsWhatsAppBSUID(s.Value) {
		ownerID, reassigned, err := models.ReassignShellContactURN(ctx, rt.DB, oa, scene.ContactID(), s.Value)
		if err != nil {
			return fmt.Errorf("error reassigning URN from shell contact: %w", err)
		}
		if reassigned {
			// the shell contact isn't part of this scene so won't be re-indexed by scene events - index it here so
			// its ES doc no longer includes the URN it just lost
			if err := indexContact(ctx, rt, oa, ownerID); err != nil {
				return fmt.Errorf("error indexing shell contact after URN reassignment: %w", err)
			}
		} else if ownerID != models.NilContactID {
			slog.Info("BSUID URN not appended because it belongs to a contact with other URNs", "urn", s.Value, "contact", scene.ContactUUID(), "owner_id", ownerID)
		}
	}

	var flowCh *core.Channel
	if channel != nil {
		flowCh = oa.SessionAssets().Channels().Get(channel.UUID())
	}

	mod := modifiers.NewRoutes([]core.Route{{URN: s.Value, Channel: flowCh}}, modifiers.RoutesAppend)
	if err := scene.ApplyModifier(ctx, rt, oa, mod, models.NilUserID, ""); err != nil {
		return fmt.Errorf("error applying routes modifier: %w", err)
	}
	return nil
}

// indexContact loads the given contact and queues it for re-indexing in Elastic
func indexContact(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactID models.ContactID) error {
	mc, err := models.LoadContact(ctx, rt.DB, oa, contactID)
	if err != nil {
		return fmt.Errorf("error loading contact: %w", err)
	}

	contact, err := mc.EngineContact(oa)
	if err != nil {
		return fmt.Errorf("error creating engine contact: %w", err)
	}

	currentFlows := map[models.ContactID]models.FlowID{mc.ID(): mc.CurrentFlowID()}
	if err := search.IndexContacts(ctx, rt, oa, []*core.Contact{contact}, currentFlows); err != nil {
		return fmt.Errorf("error indexing contact: %w", err)
	}
	return nil
}

func Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactID models.ContactID, task Task) error {
	contact, err := models.LoadContact(ctx, rt.DB, oa, contactID)
	if err != nil {
		if err == sql.ErrNoRows { // if contact no longer exists, ignore event, whatever it was gonna do is about to be deleted too
			return nil
		}
		return fmt.Errorf("error loading contact: %w", err)
	}

	return task.Perform(ctx, rt, oa, contact)
}
