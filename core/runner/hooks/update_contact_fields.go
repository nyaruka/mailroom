package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// UpdateContactFields is our hook for contact field changes
var UpdateContactFields runner.PreCommitHook = &updateContactFields{}

type updateContactFields struct{}

func (h *updateContactFields) Order() int { return 10 }

func (h *updateContactFields) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// our list of updates
	fieldUpdates := make([]any, 0, len(scenes))
	fieldDeletes := make(map[assets.FieldUUID][]any)
	for scene, args := range scenes {
		updates := make(map[assets.FieldUUID]*flows.Value, len(args))
		for _, e := range args {
			event := e.(*events.ContactFieldChanged)
			field := oa.FieldByKey(event.Field.Key)
			if field == nil {
				slog.Debug("unable to find field with key, ignoring", "session", scene.SessionUUID(), slog.Group("field", "key", event.Field.Key, "name", event.Field.Name))
				continue
			}

			updates[field.UUID()] = event.Value
		}

		// trim out deletes, adding to our list of global deletes
		for k, v := range updates {
			if v == nil || v.Text.Native() == "" {
				delete(updates, k)
				fieldDeletes[k] = append(fieldDeletes[k], &FieldDelete{
					ContactID: scene.ContactID(),
					FieldUUID: k,
				})
			}
		}

		// marshal the rest of our updates to JSON
		fieldJSON, err := json.Marshal(updates)
		if err != nil {
			return fmt.Errorf("error marshalling field values: %w", err)
		}

		// and queue them up for our update
		fieldUpdates = append(fieldUpdates, &FieldUpdate{
			ContactID: scene.ContactID(),
			Updates:   string(fieldJSON),
		})
	}

	// first apply our deletes
	// in pg9.6 we need to do this as one query per field type, in pg10 we can rewrite this to be a single query
	for _, fds := range fieldDeletes {
		err := models.BulkQuery(ctx, "deleting contact field values", tx, sqlDeleteContactFields, fds)
		if err != nil {
			return fmt.Errorf("error deleting contact fields: %w", err)
		}
	}

	// then our updates
	if len(fieldUpdates) > 0 {
		err := models.BulkQuery(ctx, "updating contact field values", tx, sqlUpdateContactFields, fieldUpdates)
		if err != nil {
			return fmt.Errorf("error updating contact fields: %w", err)
		}
	}

	return nil
}

type FieldDelete struct {
	ContactID models.ContactID `db:"contact_id"`
	FieldUUID assets.FieldUUID `db:"field_uuid"`
}

type FieldUpdate struct {
	ContactID models.ContactID `db:"contact_id"`
	Updates   string           `db:"updates"`
}

type FieldValue struct {
	Text string `json:"text"`
}

const sqlUpdateContactFields = `
UPDATE contacts_contact c
   SET fields = COALESCE(fields,'{}'::jsonb) || r.updates
  FROM (VALUES(:contact_id::int, :updates::jsonb)) AS r(contact_id, updates)
 WHERE c.id = r.contact_id`

const sqlDeleteContactFields = `
UPDATE contacts_contact c
   SET fields = fields - r.field_uuid
  FROM (VALUES(:contact_id::int, :field_uuid)) AS r(contact_id, field_uuid)
 WHERE c.id = r.contact_id`
