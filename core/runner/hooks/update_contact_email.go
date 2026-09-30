package hooks

import (
	"context"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

// UpdateContactEmail is our hook for contact email changes
var UpdateContactEmail runner.PreCommitHook = &updateContactEmail{}

type updateContactEmail struct{}

func (h *updateContactEmail) Order() int { return 10 }

func (h *updateContactEmail) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// build up our list of pairs of contact id and email
	updates := make([]*emailUpdate, 0, len(scenes))
	for s, args := range scenes {
		// we only care about the last email change
		event := args[len(args)-1].(*events.ContactEmailChanged)
		updates = append(updates, &emailUpdate{s.ContactID(), null.String(event.Email)})
	}

	// do our update
	return models.BulkQuery(ctx, "updating contact email", tx, sqlUpdateContactEmail, updates)
}

// struct used for our bulk update
type emailUpdate struct {
	ContactID models.ContactID `db:"id"`
	Email     null.String      `db:"email"`
}

// a changed address can't be assumed to be verified
const sqlUpdateContactEmail = `
UPDATE contacts_contact c
   SET email = r.email, email_verified_on = NULL
  FROM (VALUES(:id::int, :email)) AS r(id, email)
 WHERE c.id = r.id`
