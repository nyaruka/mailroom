package tallies

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	valkey "github.com/gomodule/redigo/redis"
	"github.com/lib/pq"
	"github.com/vinovest/sqlx"
)

//go:embed lua/move.lua
var luaMove string
var scriptMove = valkey.NewScript(2, luaMove)

//go:embed lua/done.lua
var luaDone string
var scriptDone = valkey.NewScript(3, luaDone)

// Retention is how long tallies whose scopes have the given prefix are kept, counted from the date which ends their
// scope. Tallies which match no retention are kept forever.
type Retention struct {
	Prefix string
	Period time.Duration
}

// Flusher writes buffered tallies to the database.
type Flusher struct {
	retentions []Retention
	batchSize  int
}

// NewFlusher creates a new flusher which flushes up to batchSize orgs per call to Flush.
func NewFlusher(retentions []Retention, batchSize int) *Flusher {
	return &Flusher{retentions: retentions, batchSize: batchSize}
}

// Flush flushes the tallies of pending orgs, returning the number of orgs flushed.
func (f *Flusher) Flush(ctx context.Context, db *sqlx.DB, vc valkey.Conn) (int, error) {
	orgIDs, err := valkey.Int64s(valkey.DoContext(vc, ctx, "SRANDMEMBER", pendingKey, f.batchSize))
	if err != nil {
		return 0, fmt.Errorf("error getting pending orgs: %w", err)
	}

	var errs []error
	for _, orgID := range orgIDs {
		if err := f.flushOrg(ctx, db, vc, OrgID(orgID)); err != nil {
			errs = append(errs, fmt.Errorf("error flushing tallies for org %d: %w", orgID, err))
		}
	}

	return len(orgIDs) - len(errs), errors.Join(errs...)
}

// flushOrg flushes one org's tallies. Each flush has a generation, which is written in the same transaction as the
// counts, so if a flush fails after committing, its retry knows not to write the counts again.
func (f *Flusher) flushOrg(ctx context.Context, db *sqlx.DB, vc valkey.Conn, orgID OrgID) error {
	lastGen, err := getGeneration(ctx, db, orgID)
	if err != nil {
		return err
	}

	// a flushing hash left over from a failed flush which did commit can only be deleted, since merging new increments
	// into it would see them skipped as already written
	leftoverGen, err := valkey.Int64(valkey.DoContext(vc, ctx, "HGET", flushingKey(orgID), generationField))
	if err == nil && leftoverGen <= lastGen {
		if _, err := valkey.DoContext(vc, ctx, "DEL", flushingKey(orgID)); err != nil {
			return fmt.Errorf("error deleting written flushing hash: %w", err)
		}
	} else if err != nil && err != valkey.ErrNil {
		return fmt.Errorf("error getting flushing hash generation: %w", err)
	}

	amounts, err := valkey.Int64Map(scriptMove.DoContext(ctx, vc, liveKey(orgID), flushingKey(orgID), lastGen+1))
	if err != nil {
		return fmt.Errorf("error moving live hash: %w", err)
	}

	if len(amounts) > 0 {
		gen := amounts[generationField]
		delete(amounts, generationField)

		if err := f.write(ctx, db, orgID, gen, amounts); err != nil {
			return err
		}
	}

	if _, err := scriptDone.DoContext(ctx, vc, liveKey(orgID), flushingKey(orgID), pendingKey, orgID); err != nil {
		return fmt.Errorf("error deleting flushing hash: %w", err)
	}
	return nil
}

const sqlUpsertTallies = `
INSERT INTO orgs_tally AS t (org_id, scope, count, expires_on)
     SELECT $1, u.scope, u.count, u.expires_on
       FROM unnest($2::text[], $3::bigint[], $4::date[]) AS u(scope, count, expires_on)
      WHERE EXISTS (SELECT 1 FROM orgs_org WHERE id = $1 AND deleted_on IS NULL)
ON CONFLICT (org_id, scope) DO UPDATE SET count = t.count + excluded.count`

const sqlSetGeneration = `
INSERT INTO orgs_tally AS t (org_id, scope, count)
     SELECT $1, $2, $3 WHERE EXISTS (SELECT 1 FROM orgs_org WHERE id = $1 AND deleted_on IS NULL)
ON CONFLICT (org_id, scope) DO UPDATE SET count = excluded.count`

// write upserts the given amounts and records their generation, unless that generation has already been written.
// Nothing is written for a deleted org.
func (f *Flusher) write(ctx context.Context, db *sqlx.DB, orgID OrgID, gen int64, amounts map[string]int64) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var lastGen int64
	err = tx.GetContext(ctx, &lastGen, `SELECT count FROM orgs_tally WHERE org_id = $1 AND scope = $2 FOR UPDATE`, orgID, generationField)
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
		expiresOn = append(expiresOn, f.expiresOn(scope))
	}

	if _, err := tx.ExecContext(ctx, sqlUpsertTallies, orgID, pq.Array(scopes), pq.Array(counts), pq.Array(expiresOn)); err != nil {
		return fmt.Errorf("error upserting tallies: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlSetGeneration, orgID, generationField, gen); err != nil {
		return fmt.Errorf("error setting generation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing transaction: %w", err)
	}
	return nil
}

// expiresOn gets the expiry date of a tally from the first retention matching its scope.
func (f *Flusher) expiresOn(scope string) sql.NullString {
	for _, r := range f.retentions {
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

func getGeneration(ctx context.Context, db *sqlx.DB, orgID OrgID) (int64, error) {
	var gen int64
	err := db.GetContext(ctx, &gen, `SELECT count FROM orgs_tally WHERE org_id = $1 AND scope = $2`, orgID, generationField)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("error getting generation: %w", err)
	}
	return gen, nil
}
