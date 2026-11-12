package models

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/vinovest/sqlx"
)

const (
	// TallyEmailsShared is the daily number of emails sent using the shared email configuration, counting each
	// recipient of a send
	TallyEmailsShared = "emails/shared"

	// TallyMsgs prefixes the daily counts of messages with a channel, e.g. msgs/voice:out@2026-11-03, and of messages
	// which failed for having no destination, e.g. msgs/text:nodest@2026-11-03
	TallyMsgs = "msgs"

	// TallyContactsNew prefixes the daily counts of contacts created, by how they came to exist, e.g.
	// contacts/new:inbound@2026-11-03. Deleting a contact doesn't reduce them.
	TallyContactsNew = "contacts/new"

	// TallyLLMs prefixes the daily usage tallies which LLM services can record, e.g. llms/foo/tokens:in@2026-11-02
	TallyLLMs = "llms"
)

// legacyTallyChannelMsgs prefixes the org tallies which counted messages on each channel before they became channel
// tallies, e.g. channels/12/text:in@2026-11-03, which may still need flushing
const legacyTallyChannelMsgs = "channels"

// tallyRetention is how long tallies are kept after they're first written, by owner type and scope prefix. Tallies
// without one, such as the org msgs/ ones, are kept forever.
var tallyRetention = map[tallyOwner]map[string]time.Duration{
	tallyOwnerOrg: {
		TallyEmailsShared + "@":      90 * 24 * time.Hour,
		legacyTallyChannelMsgs + "/": 400 * 24 * time.Hour,
		TallyLLMs + "/":              90 * 24 * time.Hour,
	},
	tallyOwnerFlow: {
		flowMsgsInDateScope: 365 * 24 * time.Hour,
	},
	tallyOwnerChannel: {
		"": 400 * 24 * time.Hour, // e.g. text:in@2026-11-03
	},
}

// flowMsgsInDateScope prefixes the daily counts of incoming messages handled by a flow, e.g. msgsin:date:2026-11-03
const flowMsgsInDateScope = "msgsin:date:"

// TallyField returns the field of the tally of the given scope for the given org, which if day isn't zero is the daily
// tally for its UTC day. Panics if the scope contains @ as it's reserved for separating the day.
func TallyField(orgID OrgID, scope string, day time.Time) string {
	return fmt.Sprintf("%c%d|%s", tallyOwnerOrg, orgID, datedTallyScope(scope, day))
}

// ChannelTallyField returns the field of the tally of the given scope for the given channel, which if day isn't zero is
// the daily tally for its UTC day, e.g. C12|text:in@2026-11-03. Panics if the scope contains @ as it's reserved for
// separating the day.
func ChannelTallyField(channelID ChannelID, scope string, day time.Time) string {
	return fmt.Sprintf("%c%d|%s", tallyOwnerChannel, channelID, datedTallyScope(scope, day))
}

func datedTallyScope(scope string, day time.Time) string {
	if !day.IsZero() {
		if strings.Contains(scope, "@") {
			panic(fmt.Sprintf("tally scope %q can't contain @", scope))
		}
		scope += "@" + day.UTC().Format(time.DateOnly)
	}
	return scope
}

// FlowTallyField returns the field of the tally of the given scope for the given flow
func FlowTallyField(flowID FlowID, scope string) string {
	return fmt.Sprintf("%c%d|%s", tallyOwnerFlow, flowID, scope)
}

// FlowSegmentTallyScope returns the scope of the tally which counts contacts taking the given exit to the given node,
// which is the raw bytes of the two UUIDs encoded as unpadded base64url to keep keys of the tallies table compact. The
// UUIDs must be valid, as the flow definitions they come from are validated.
func FlowSegmentTallyScope(exitUUID flows.ExitUUID, destUUID core.NodeUUID) string {
	exit, dest := uuid.MustParse(string(exitUUID)), uuid.MustParse(string(destUUID))
	return "segment:" + base64.RawURLEncoding.EncodeToString(append(exit[:], dest[:]...))
}

// FlowMsgsInTallies gets the tally amounts which count an incoming message handled by the given flow at the given time,
// by its UTC hour of day and its UTC day
func FlowMsgsInTallies(flowID FlowID, t time.Time) map[string]int64 {
	t = t.UTC()
	return map[string]int64{
		FlowTallyField(flowID, fmt.Sprintf("msgsin:hour:%d", t.Hour())):     1,
		FlowTallyField(flowID, flowMsgsInDateScope+t.Format(time.DateOnly)): 1,
	}
}

// tallyOwner is the type of thing a tally belongs to, which prefixes its id in a tally field
type tallyOwner byte

const (
	tallyOwnerOrg     tallyOwner = 'O'
	tallyOwnerFlow    tallyOwner = 'F'
	tallyOwnerChannel tallyOwner = 'C'
)

// parseTallyField parses a tally field into its owner type, owner id and scope. Fields are <owner><id>|<scope>, where
// owner is O for an org tally, F for a flow tally or C for a channel tally, e.g. O123|msgs/text:in@2026-11-03,
// F45|segment:Qq9uGVPvT9mCMvICo-oCKx1PJbLGSEbMgRa-Dsa200o or C67|text:in@2026-11-03.
func parseTallyField(field string) (tallyOwner, int64, string, error) {
	ownerStr, scope, ok := strings.Cut(field, "|")
	if ok && len(ownerStr) > 1 {
		owner := tallyOwner(ownerStr[0])
		id, err := strconv.ParseUint(ownerStr[1:], 10, 63)
		if (owner == tallyOwnerOrg || owner == tallyOwnerFlow || owner == tallyOwnerChannel) && err == nil && id > 0 {
			return owner, int64(id), scope, nil
		}
	}
	return 0, 0, "", fmt.Errorf("invalid tally field %q", field)
}

// tallyRetentionFor returns the retention of the longest prefix of the given owner type's scope which has one, or zero
// if none do
func tallyRetentionFor(owner tallyOwner, scope string) time.Duration {
	var longest string
	for prefix := range tallyRetention[owner] {
		if strings.HasPrefix(scope, prefix) && len(prefix) > len(longest) {
			longest = prefix
		}
	}
	return tallyRetention[owner][longest]
}

// tallyDaySeparator is what precedes the day which a daily tally scope ends with, by owner type
var tallyDaySeparator = map[tallyOwner]string{tallyOwnerOrg: "@", tallyOwnerFlow: ":", tallyOwnerChannel: "@"}

// tallyDay returns the day which a daily tally scope ends with, or the given default if the scope isn't a daily one.
// Expiries are counted from it so that they don't depend on when a tally happens to be written.
func tallyDay(owner tallyOwner, scope string, def time.Time) time.Time {
	if i := strings.LastIndex(scope, tallyDaySeparator[owner]); i >= 0 {
		if day, err := time.Parse(time.DateOnly, scope[i+1:]); err == nil {
			return day
		}
	}
	return def
}

// TallyStore is a vkutil.TallyStore which writes each owner type's tallies to its own table, e.g. org tallies to
// orgs_orgtally, recording the last batch written for its key base in orgs_tallyflush. Nothing is written for an owner
// which isn't active, and tallies with a total of zero aren't kept.
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

// tallyTable is a table of tallies for one owner type, keyed by owner and scope
type tallyTable struct {
	sqlUpsert     string
	sqlDeleteZero string
}

func newTallyTable(table, ownerCol, ownerTable string) *tallyTable {
	return &tallyTable{
		// expiry is set by every write so that an undated tally's is extended by each one
		sqlUpsert: fmt.Sprintf(`
INSERT INTO %[1]s AS t (%[2]s, scope, count, expires_on)
     SELECT u.owner_id, u.scope, u.count, u.expires_on
       FROM unnest($1::bigint[], $2::text[], $3::bigint[], $4::date[]) AS u(owner_id, scope, count, expires_on)
       JOIN %[3]s o ON o.id = u.owner_id AND o.is_active
   ORDER BY u.owner_id, u.scope
ON CONFLICT (%[2]s, scope) DO UPDATE SET count = t.count + excluded.count, expires_on = excluded.expires_on`, table, ownerCol, ownerTable),

		// a separate statement because a data-modifying CTE can't safely delete rows which the same statement's upsert
		// modified
		sqlDeleteZero: fmt.Sprintf(`
DELETE FROM %[1]s t
      USING unnest($1::bigint[], $2::text[]) AS u(owner_id, scope), %[3]s o
      WHERE t.%[2]s = u.owner_id AND t.scope = u.scope AND t.count = 0 AND o.id = t.%[2]s AND o.is_active`, table, ownerCol, ownerTable),
	}
}

// the table of each owner type, in the order they're written so that concurrent writes lock rows in the same order
var tallyTables = []struct {
	owner tallyOwner
	table *tallyTable
}{
	{tallyOwnerOrg, newTallyTable("orgs_orgtally", "org_id", "orgs_org")},
	{tallyOwnerFlow, newTallyTable("flows_flowtally", "flow_id", "flows_flow")},
	{tallyOwnerChannel, newTallyTable("channels_channeltally", "channel_id", "channels_channel")},
}

// tallyRows are the amounts to write to a tally table
type tallyRows struct {
	ownerIDs    []int64
	scopes      []string
	counts      []int64
	expiresOn   []sql.NullString
	decOwnerIDs []int64 // only a decrement can bring a tally to zero
	decScopes   []string
}

func (r *tallyRows) add(ownerID int64, scope string, count int64, expires sql.NullString) {
	r.ownerIDs = append(r.ownerIDs, ownerID)
	r.scopes = append(r.scopes, scope)
	r.counts = append(r.counts, count)
	r.expiresOn = append(r.expiresOn, expires)
	if count < 0 {
		r.decOwnerIDs = append(r.decOwnerIDs, ownerID)
		r.decScopes = append(r.decScopes, scope)
	}
}

func (t *tallyTable) write(ctx context.Context, tx *sqlx.Tx, r *tallyRows) error {
	if len(r.ownerIDs) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, t.sqlUpsert, pq.Array(r.ownerIDs), pq.Array(r.scopes), pq.Array(r.counts), pq.Array(r.expiresOn)); err != nil {
		return fmt.Errorf("error upserting tallies: %w", err)
	}
	if len(r.decOwnerIDs) > 0 {
		if _, err := tx.ExecContext(ctx, t.sqlDeleteZero, pq.Array(r.decOwnerIDs), pq.Array(r.decScopes)); err != nil {
			return fmt.Errorf("error deleting zero tallies: %w", err)
		}
	}
	return nil
}

type tallyKey struct {
	owner   tallyOwner
	ownerID int64
	scope   string
}

// Write adds the given amounts by field to tallies and records the batch, unless it has already been written. Tallies
// which the batch brings to zero are deleted.
func (s *TallyStore) Write(ctx context.Context, id int64, amounts map[string]int64) (bool, error) {
	byKey := make(map[tallyKey]int64, len(amounts))
	for field, count := range amounts {
		owner, ownerID, scope, err := parseTallyField(field)
		if err != nil {
			return false, err
		}
		byKey[tallyKey{owner, ownerID, scope}] += count
	}

	rows := make(map[tallyOwner]*tallyRows, len(tallyTables))
	for _, tt := range tallyTables {
		rows[tt.owner] = &tallyRows{}
	}
	today := dates.Now().UTC().Truncate(24 * time.Hour)

	for key, count := range byKey {
		if count == 0 {
			continue
		}

		var expires sql.NullString
		if retention := tallyRetentionFor(key.owner, key.scope); retention != 0 {
			expiry := tallyDay(key.owner, key.scope, today).Add(retention)
			if expiry.Before(today) {
				continue // already past retention
			}
			expires = sql.NullString{String: expiry.Format(time.DateOnly), Valid: true}
		}

		rows[key.owner].add(key.ownerID, key.scope, count, expires)
	}

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

	for _, tt := range tallyTables {
		if err := tt.table.write(ctx, tx, rows[tt.owner]); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("error committing transaction: %w", err)
	}
	return true, nil
}
