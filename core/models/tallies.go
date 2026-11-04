package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/tallies"
	"github.com/vinovest/sqlx"
)

const (
	// TallyEmailsShared is the daily number of emails sent using the shared email configuration, counting each
	// recipient of a send
	TallyEmailsShared = "emails/shared"

	// TallyChannelMsgs prefixes the daily counts of messages (not segments) on each channel, e.g. channels/12/text:in@2026-11-03
	TallyChannelMsgs = "channels"

	// TallyMsgs prefixes the daily counts of messages with a channel, e.g. msgs/voice:out@2026-11-03
	TallyMsgs = "msgs"

	// TallyLLMs prefixes the daily usage tallies which LLM services can record, e.g. llms/foo/tokens:in@2026-11-02
	TallyLLMs = "llms"
)

// tallyRetention is how long tallies are kept after they're first written, by scope prefix. Tallies without one, such
// as the msgs/ ones, are kept forever.
var tallyRetention = map[string]time.Duration{
	TallyEmailsShared + "@": 90 * 24 * time.Hour,
	TallyChannelMsgs + "/":  2 * 365 * 24 * time.Hour,
	TallyLLMs + "/":         90 * 24 * time.Hour,
}

// tallyRetentionFor returns the retention of the longest prefix of the given scope which has one, or zero if none do
func tallyRetentionFor(scope string) time.Duration {
	var longest string
	for prefix := range tallyRetention {
		if strings.HasPrefix(scope, prefix) && len(prefix) > len(longest) {
			longest = prefix
		}
	}
	return tallyRetention[longest]
}

// TallyStore is a tallies.Store which writes to the orgs_tally table, recording the last batch written for its key
// base in orgs_tallyflush. Nothing is written for an org which isn't active.
type TallyStore struct {
	db      *sqlx.DB
	keyBase string
}

// NewTallyStore creates a new tally store for tallies with the given key base
func NewTallyStore(db *sqlx.DB, keyBase string) *TallyStore {
	return &TallyStore{db: db, keyBase: keyBase}
}

func (s *TallyStore) LastBatch(ctx context.Context) (int64, error) {
	var lastBatch int64
	err := s.db.GetContext(ctx, &lastBatch, `SELECT last_batch FROM orgs_tallyflush WHERE name = $1`, s.keyBase)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("error getting last batch: %w", err)
	}
	return lastBatch, nil
}

// conditional update so that only one write of each batch applies, even if two are made concurrently
const sqlClaimTallyBatch = `
INSERT INTO orgs_tallyflush (name, last_batch) VALUES ($1, $2)
ON CONFLICT (name) DO UPDATE SET last_batch = excluded.last_batch WHERE orgs_tallyflush.last_batch < excluded.last_batch
RETURNING 1`

const sqlUpsertTallies = `
INSERT INTO orgs_tally AS t (org_id, scope, count, expires_on)
     SELECT u.org_id, u.scope, u.count, u.expires_on
       FROM unnest($1::int[], $2::text[], $3::bigint[], $4::date[]) AS u(org_id, scope, count, expires_on)
       JOIN orgs_org o ON o.id = u.org_id AND o.is_active
   ORDER BY u.org_id, u.scope
ON CONFLICT (org_id, scope) DO UPDATE SET count = t.count + excluded.count`

// Write adds the given amounts to org tallies and records the batch, unless it has already been written, setting
// expiries only on tallies which don't exist yet
func (s *TallyStore) Write(ctx context.Context, id int64, amounts []tallies.Amount) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var claimed int
	if err := tx.GetContext(ctx, &claimed, sqlClaimTallyBatch, s.keyBase, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("error recording batch: %w", err)
	}

	today := dates.Now().UTC().Truncate(24 * time.Hour)
	orgIDs := make([]int64, len(amounts))
	scopes := make([]string, len(amounts))
	counts := make([]int64, len(amounts))
	expiresOn := make([]sql.NullString, len(amounts))
	for i, a := range amounts {
		orgIDs[i], scopes[i], counts[i] = int64(a.Owner), a.Scope, a.Count
		if retention := tallyRetentionFor(a.Scope); retention != 0 {
			expiresOn[i] = sql.NullString{String: today.Add(retention).Format(time.DateOnly), Valid: true}
		}
	}

	_, err = tx.ExecContext(ctx, sqlUpsertTallies, pq.Array(orgIDs), pq.Array(scopes), pq.Array(counts), pq.Array(expiresOn))
	if err != nil {
		return false, fmt.Errorf("error upserting tallies: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("error committing transaction: %w", err)
	}
	return true, nil
}
