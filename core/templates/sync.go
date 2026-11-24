package templates

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
	"github.com/nyaruka/vkutil/locks"
	"github.com/vinovest/sqlx"
)

const (
	// the same key as temba's own template sync, so that the two never sync a channel at the same time
	syncLockKey = "refresh_channel_templates_%d"
	syncLockTTL = 10 * time.Minute

	// how long we give recording the outcome, which happens even if the context we were given is done
	recordTimeout = 15 * time.Second
)

// ErrSyncInProgress is returned when another sync of the channel's templates is already running
var ErrSyncInProgress = errors.New("templates are already being synced")

// FetchError is returned when the templates couldn't be fetched from the provider
type FetchError struct {
	err error
}

func (e *FetchError) Error() string { return fmt.Sprintf("error fetching templates: %s", e.err) }
func (e *FetchError) Unwrap() error { return e.err }

// SyncChannel fetches the given channel's templates from its provider and updates its translations to match,
// returning how many translations it has.
func SyncChannel(ctx context.Context, rt *runtime.Runtime, channel *models.Channel) (int, error) {
	svc, err := GetService(rt.Config, rt.HTTP.Services, channel)
	if err != nil {
		return 0, err
	}

	locker := locks.NewLocker(fmt.Sprintf(syncLockKey, channel.ID()), syncLockTTL)
	lock, err := locker.Grab(ctx, rt.VK, 0)
	if err != nil {
		return 0, fmt.Errorf("error grabbing templates sync lock: %w", err)
	}
	if lock == "" {
		return 0, ErrSyncInProgress
	}
	defer locker.Release(context.WithoutCancel(ctx), rt.VK, lock)

	clog := models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))

	fetched, fetchErr := svc.FetchTemplates(ctx, clog)
	if fetchErr != nil {
		clog.Error(&svclogs.Error{Message: fetchErr.Error()})
	} else {
		removeTooLong(fetched)

		for _, invalid := range fetched.Invalid {
			clog.Error(&svclogs.Error{Message: invalid.Error()})
		}
	}

	clog.End()

	if _, err := rt.Dynamo.Main.Queue(clog); err != nil {
		return 0, fmt.Errorf("error queuing templates sync channel log: %w", err)
	}

	// the fetch may have used up our context, e.g. a request's deadline, but we still want to record the outcome
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()

	if fetchErr != nil {
		if err := models.UpdateChannelTemplatesSyncState(recordCtx, rt.DB, channel.ID(), dates.Now(), true); err != nil {
			return 0, err
		}
		return 0, &FetchError{fetchErr}
	}

	numTranslations, err := recordSuccess(recordCtx, rt, channel, fetched)
	if err != nil {
		return 0, err
	}

	return numTranslations, nil
}

func recordSuccess(ctx context.Context, rt *runtime.Runtime, channel *models.Channel, fetched *Fetched) (int, error) {
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("error starting transaction: %w", err)
	}
	defer tx.Rollback()

	numTranslations, err := updateTranslations(ctx, tx, channel, fetched)
	if err != nil {
		return 0, err
	}

	if err := models.UpdateChannelTemplatesSyncState(ctx, tx, channel.ID(), dates.Now(), false); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("error committing templates sync: %w", err)
	}

	return numTranslations, nil
}

// removeTooLong moves translations which won't fit in the database to the invalid templates
func removeTooLong(fetched *Fetched) {
	fetched.Translations = slices.DeleteFunc(fetched.Translations, func(tr *Translation) bool {
		if utf8.RuneCountInString(tr.Name) > 512 || len(tr.Locale) > 6 || utf8.RuneCountInString(tr.ExternalLocale) > 6 ||
			utf8.RuneCountInString(tr.Namespace) > 36 || utf8.RuneCountInString(tr.ExternalID) > 64 {
			err := fmt.Errorf("translation %s has a field that is too long", tr.ExternalLocale)
			fetched.Invalid = append(fetched.Invalid, &InvalidTemplateError{Name: tr.Name, Err: err})
			return true
		}
		return false
	})
}

const sqlGetOrCreateTemplate = `
WITH created AS (
    INSERT INTO templates_template(uuid, org_id, name, is_system, created_on, modified_on, created_by_id, modified_by_id)
         SELECT $1, o.id, $3, FALSE, NOW(), NOW(), o.created_by_id, o.created_by_id FROM orgs_org o WHERE o.id = $2
    ON CONFLICT (org_id, name) DO NOTHING
      RETURNING id
)
SELECT id FROM created
 UNION ALL
SELECT id FROM templates_template WHERE org_id = $2 AND name = $3
 LIMIT 1`

// if another transaction created the template while we were trying to, the statement above can't see it
const sqlSelectTemplate = `SELECT id FROM templates_template WHERE org_id = $1 AND name = $2`

// updates a translation that has changed, and returns its id and whether it was created or changed. An empty status
// keeps the stored one.
const sqlUpsertTranslation = `
WITH upserted AS (
    INSERT INTO templates_templatetranslation AS t(template_id, channel_id, locale, components, variables, status, namespace,
                                                   external_id, external_locale, is_supported, is_compatible)
         VALUES($1, $2, $3, $4::jsonb, $5::jsonb, COALESCE(NULLIF($6, ''), 'P'), $7, $8, $9, $10, TRUE)
    ON CONFLICT (template_id, channel_id, locale) DO UPDATE
            SET components = EXCLUDED.components, variables = EXCLUDED.variables,
                status = COALESCE(NULLIF($6, ''), t.status), namespace = EXCLUDED.namespace, external_id = EXCLUDED.external_id,
                external_locale = EXCLUDED.external_locale, is_supported = EXCLUDED.is_supported
          WHERE (t.components, t.variables, t.status, t.namespace, t.external_id, t.external_locale, t.is_supported)
                IS DISTINCT FROM
                (EXCLUDED.components, EXCLUDED.variables, COALESCE(NULLIF($6, ''), t.status), EXCLUDED.namespace,
                 EXCLUDED.external_id, EXCLUDED.external_locale, EXCLUDED.is_supported)
      RETURNING id
)
SELECT id, TRUE AS changed FROM upserted
 UNION ALL
SELECT id, FALSE AS changed
  FROM templates_templatetranslation
 WHERE template_id = $1 AND channel_id = $2 AND locale = $3 AND NOT EXISTS (SELECT 1 FROM upserted)`

// the channel's translations we didn't see, leaving those of templates we couldn't parse
const sqlUnseenTranslations = `
SELECT tr.id
  FROM templates_templatetranslation tr
  JOIN templates_template t ON t.id = tr.template_id
 WHERE tr.channel_id = $1 AND NOT (tr.id = ANY($2)) AND NOT (t.name = ANY($3))`

// templates can't keep a base translation that we're about to delete
const sqlClearDeletedBases = `
UPDATE templates_template SET base_translation_id = NULL WHERE base_translation_id = ANY($1)`

const sqlDeleteTranslations = `
DELETE FROM templates_templatetranslation WHERE id = ANY($1) RETURNING template_id`

// a template's base translation is the one in the workspace's primary language, otherwise its oldest
const sqlUpdateMissingBases = `
UPDATE templates_template t
   SET base_translation_id = (
           SELECT tr.id
             FROM templates_templatetranslation tr
            WHERE tr.template_id = t.id
         ORDER BY COALESCE(tr.locale LIKE o.flow_languages[1] || '%', FALSE) DESC, tr.id
            LIMIT 1
       ),
       modified_on = NOW()
  FROM orgs_org o
 WHERE t.id = ANY($1) AND t.base_translation_id IS NULL AND o.id = t.org_id`

// a translation is compatible if it has the same variables as its template's base translation
const sqlUpdateCompatibility = `
WITH updated AS (
    UPDATE templates_templatetranslation tr
       SET is_compatible = NOT tr.is_compatible
      FROM templates_template t
 LEFT JOIN templates_templatetranslation b ON b.id = t.base_translation_id
     WHERE tr.template_id = t.id AND t.id = ANY($1)
       AND tr.is_compatible != (tr.variables = COALESCE(b.variables, '[]'::jsonb))
 RETURNING tr.template_id
)
UPDATE templates_template SET modified_on = NOW() WHERE id IN (SELECT template_id FROM updated)`

const sqlTouchTemplates = `UPDATE templates_template SET modified_on = NOW() WHERE id = ANY($1)`

// updates the channel's translations to match those fetched, returning how many it has
func updateTranslations(ctx context.Context, tx *sqlx.Tx, channel *models.Channel, fetched *Fetched) (int, error) {
	trs := fetched.Translations
	seen := make([]int64, 0, len(trs))
	touched := make([]int64, 0, len(trs)) // templates whose translations we've seen or deleted
	changed := make([]int64, 0)           // templates whose translations we've changed

	for _, tr := range trs {
		templateID, err := getOrCreateTemplate(ctx, tx, channel.OrgID(), tr.Name)
		if err != nil {
			return 0, err
		}

		var row struct {
			ID      int64 `db:"id"`
			Changed bool  `db:"changed"`
		}
		err = tx.GetContext(ctx, &row, sqlUpsertTranslation,
			templateID, channel.ID(), tr.Locale, string(jsonx.MustMarshal(tr.Components)), string(jsonx.MustMarshal(tr.Variables)),
			tr.Status, tr.Namespace, null.String(tr.ExternalID), tr.ExternalLocale, tr.IsSupported,
		)
		if err != nil {
			return 0, fmt.Errorf("error upserting translation %s/%s: %w", tr.Name, tr.Locale, err)
		}

		seen = append(seen, row.ID)
		touched = append(touched, templateID)
		if row.Changed {
			changed = append(changed, templateID)
		}
	}

	invalidNames := make([]string, len(fetched.Invalid))
	for i, invalid := range fetched.Invalid {
		invalidNames[i] = invalid.Name
	}

	var unseen []int64
	err := tx.SelectContext(ctx, &unseen, sqlUnseenTranslations, channel.ID(), pq.Array(seen), pq.Array(invalidNames))
	if err != nil {
		return 0, fmt.Errorf("error selecting unseen translations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlClearDeletedBases, pq.Array(unseen)); err != nil {
		return 0, fmt.Errorf("error clearing base translations: %w", err)
	}

	var deletedFrom []int64
	if err := tx.SelectContext(ctx, &deletedFrom, sqlDeleteTranslations, pq.Array(unseen)); err != nil {
		return 0, fmt.Errorf("error deleting translations: %w", err)
	}
	touched = append(touched, deletedFrom...)
	changed = append(changed, deletedFrom...)

	if _, err := tx.ExecContext(ctx, sqlUpdateMissingBases, pq.Array(touched)); err != nil {
		return 0, fmt.Errorf("error updating base translations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlUpdateCompatibility, pq.Array(touched)); err != nil {
		return 0, fmt.Errorf("error updating translation compatibility: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlTouchTemplates, pq.Array(changed)); err != nil {
		return 0, fmt.Errorf("error updating templates: %w", err)
	}

	slices.Sort(seen)
	return len(slices.Compact(seen)), nil
}

func getOrCreateTemplate(ctx context.Context, tx *sqlx.Tx, orgID models.OrgID, name string) (int64, error) {
	var id int64
	err := tx.GetContext(ctx, &id, sqlGetOrCreateTemplate, uuids.NewV4(), orgID, name)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.GetContext(ctx, &id, sqlSelectTemplate, orgID, name)
	}
	if err != nil {
		return 0, fmt.Errorf("error getting or creating template %s: %w", name, err)
	}
	return id, nil
}
