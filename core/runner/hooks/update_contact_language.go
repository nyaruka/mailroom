package hooks

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

// UpdateContactLanguage is our hook for contact language changes
var UpdateContactLanguage runner.PreCommitHook = &updateContactLanguage{}

type updateContactLanguage struct{}

func (h *updateContactLanguage) Order() int { return 1 }

func (h *updateContactLanguage) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// build up our list of pairs of contact id and language name
	updates := make([]*languageUpdate, 0, len(scenes))
	for s, e := range scenes {
		// we only care about the last name change
		event := e[len(e)-1].(*events.ContactLanguageChangedEvent)
		updates = append(updates, &languageUpdate{s.ContactID(), null.String(event.Language)})
	}

	// do our update
	return models.BulkQuery(ctx, "updating contact language", tx, sqlUpdateContactLanguage, updates)
}

// struct used for our bulk update
type languageUpdate struct {
	ContactID models.ContactID `db:"id"`
	Language  null.String      `db:"language"`
}

const sqlUpdateContactLanguage = `
UPDATE contacts_contact c
   SET language = r.language
  FROM (VALUES(:id, :language)) AS r(id, language)
 WHERE c.id = r.id::int`
