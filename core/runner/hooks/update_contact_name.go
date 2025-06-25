package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

// UpdateContactName is our hook for contact name changes
var UpdateContactName runner.PreCommitHook = &updateContactName{}

type updateContactName struct{}

func (h *updateContactName) Order() int { return 1 }

func (h *updateContactName) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// build up our list of pairs of contact id and contact name
	updates := make([]*nameUpdate, 0, len(scenes))
	for s, e := range scenes {
		// we only care about the last name change
		event := e[len(e)-1].(*events.ContactNameChanged)
		updates = append(updates, &nameUpdate{s.ContactID(), null.String(fmt.Sprintf("%.128s", event.Name))})
	}

	return models.BulkQuery(ctx, "updating contact name", tx, sqlUpdateContactName, updates)
}

// struct used for our bulk insert
type nameUpdate struct {
	ContactID models.ContactID `db:"id"`
	Name      null.String      `db:"name"`
}

const sqlUpdateContactName = `
UPDATE contacts_contact c
   SET name = r.name
  FROM (VALUES(:id, :name)) AS r(id, name)
 WHERE c.id = r.id::int`
