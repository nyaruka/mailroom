package models

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/tallies"
	"github.com/vinovest/sqlx"
)

const (
	// TallyEmailsShared is the daily number of emails sent using the shared email configuration, counting each
	// recipient of a send
	TallyEmailsShared = "emails/shared"

	// TallyLLMs prefixes the daily usage tallies of each LLM, e.g. llms/123/tokens:in/2026-11-02
	TallyLLMs = "llms"

	// scope of the row which records the last flush generation written for an org
	tallyGenerationScope = "_gen"
)

// TallyRetention is how long tallies whose scopes have the given prefix are kept, counted from the date which ends
// their scope. Tallies which match no retention are kept forever.
type TallyRetention struct {
	Prefix string
	Period time.Duration
}

// TallyRetentions are how long daily tallies are kept
var TallyRetentions = []TallyRetention{
	{Prefix: TallyEmailsShared + "/", Period: 90 * 24 * time.Hour},
	{Prefix: TallyLLMs + "/", Period: 90 * 24 * time.Hour},
}

// TallyStore is a tallies.Store which writes to the orgs_tally table. Nothing is written for a deleted org.
type TallyStore struct {
	db         *sqlx.DB
	retentions []TallyRetention
}

// NewTallyStore creates a new tally store
func NewTallyStore(db *sqlx.DB, retentions []TallyRetention) *TallyStore {
	return &TallyStore{db: db, retentions: retentions}
}

func (s *TallyStore) LastGeneration(ctx context.Context, owner tallies.OwnerID) (int64, error) {
	var gen int64
	err := s.db.GetContext(ctx, &gen, `SELECT count FROM orgs_tally WHERE org_id = $1 AND scope = $2`, owner, tallyGenerationScope)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("error getting generation: %w", err)
	}
	return gen, nil
}

const sqlUpsertTallies = `
INSERT INTO orgs_tally AS t (org_id, scope, count, expires_on)
     SELECT $1, u.scope, u.count, u.expires_on
       FROM unnest($2::text[], $3::bigint[], $4::date[]) AS u(scope, count, expires_on)
      WHERE EXISTS (SELECT 1 FROM orgs_org WHERE id = $1 AND deleted_on IS NULL)
ON CONFLICT (org_id, scope) DO UPDATE SET count = t.count + excluded.count`

const sqlSetTallyGeneration = `
INSERT INTO orgs_tally AS t (org_id, scope, count)
     SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM orgs_org WHERE id = $1 AND deleted_on IS NULL)
ON CONFLICT (org_id, scope) DO UPDATE SET count = excluded.count`

func (s *TallyStore) Write(ctx context.Context, owner tallies.OwnerID, gen int64, amounts map[string]int64) error {
	if _, ok := amounts[tallyGenerationScope]; ok {
		return fmt.Errorf("%s is a reserved scope", tallyGenerationScope)
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var lastGen int64
	err = tx.GetContext(ctx, &lastGen, `SELECT count FROM orgs_tally WHERE org_id = $1 AND scope = $2 FOR UPDATE`, owner, tallyGenerationScope)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("error locking generation: %w", err)
	}
	if lastGen >= gen {
		return nil
	}

	scopes := make([]string, 0, len(amounts))
	counts := make([]int64, 0, len(amounts))
	expiresOn := make([]sql.NullString, 0, len(amounts))
	for scope, count := range amounts {
		scopes = append(scopes, scope)
		counts = append(counts, count)
		expiresOn = append(expiresOn, s.expiresOn(scope))
	}

	if _, err := tx.ExecContext(ctx, sqlUpsertTallies, owner, pq.Array(scopes), pq.Array(counts), pq.Array(expiresOn)); err != nil {
		return fmt.Errorf("error upserting tallies: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlSetTallyGeneration, owner, tallyGenerationScope, gen); err != nil {
		return fmt.Errorf("error setting generation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}
	return nil
}

// expiresOn gets the expiry date of a tally from the first retention matching its scope
func (s *TallyStore) expiresOn(scope string) sql.NullString {
	for _, r := range s.retentions {
		if strings.HasPrefix(scope, r.Prefix) {
			day, err := time.Parse(time.DateOnly, scope[strings.LastIndexByte(scope, '/')+1:])
			if err != nil {
				return sql.NullString{}
			}
			return sql.NullString{String: day.Add(r.Period).Format(time.DateOnly), Valid: true}
		}
	}
	return sql.NullString{}
}
