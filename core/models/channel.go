package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/null/v3"
	"github.com/vinovest/sqlx"
)

// ChannelID is the type for channel IDs
type ChannelID int

// NilChannelID is the nil value for channel IDs
const NilChannelID = ChannelID(0)

// ChannelType is the type for the type of a channel
type ChannelType string

// channel type constants
const (
	ChannelTypeAndroid = ChannelType("A")
)

// config key constants
const (
	ChannelConfigCallbackDomain     = "callback_domain"
	ChannelConfigMaxConcurrentCalls = "max_concurrent_calls"
	ChannelConfigFCMID              = "FCM_ID"
)

// Channel is the mailroom struct that represents channels
type Channel struct {
	ID_                 ChannelID            `json:"id"`
	UUID_               assets.ChannelUUID   `json:"uuid"`
	OrgID_              OrgID                `json:"org_id"`
	Name_               string               `json:"name"`
	Address_            string               `json:"address"`
	Type_               ChannelType          `json:"channel_type"`
	TPS_                int                  `json:"tps"`
	Country_            null.String          `json:"country"`
	Schemes_            []string             `json:"schemes"`
	Roles_              []assets.ChannelRole `json:"roles"`
	MatchPrefixes_      []string             `json:"match_prefixes"`
	AllowInternational_ bool                 `json:"allow_international"`
	MachineDetection_   bool                 `json:"machine_detection"`
	Config_             Config               `json:"config"`
}

// ID returns the id of this channel
func (c *Channel) ID() ChannelID { return c.ID_ }

// OrgID returns the org id of this channel
func (c *Channel) OrgID() OrgID { return c.OrgID_ }

// UUID returns the UUID of this channel
func (c *Channel) UUID() assets.ChannelUUID { return c.UUID_ }

// Name returns the name of this channel
func (c *Channel) Name() string { return c.Name_ }

// Type returns the channel type for this channel
func (c *Channel) Type() ChannelType { return c.Type_ }

// Type returns the channel type for this channel
func (c *Channel) IsAndroid() bool { return c.Type_ == ChannelTypeAndroid }

// TPS returns the max number of transactions per second this channel supports
func (c *Channel) TPS() int { return c.TPS_ }

// Address returns the name of this channel
func (c *Channel) Address() string { return c.Address_ }

// Country returns the contry code for this channel
func (c *Channel) Country() i18n.Country { return i18n.Country(string(c.Country_)) }

// Schemes returns the schemes this channel supports
func (c *Channel) Schemes() []string { return c.Schemes_ }

// Roles returns the roles this channel supports
func (c *Channel) Roles() []assets.ChannelRole { return c.Roles_ }

// MatchPrefixes returns the prefixes we should also match when determining channel affinity
func (c *Channel) MatchPrefixes() []string { return c.MatchPrefixes_ }

// AllowInternational returns whether this channel allows sending internationally (only applies to TEL schemes)
func (c *Channel) AllowInternational() bool { return c.AllowInternational_ }

// MachineDetection returns whether this channel should do answering machine detection (only applies to IVR)
func (c *Channel) MachineDetection() bool { return c.MachineDetection_ }

// Config returns the config for this channel
func (c *Channel) Config() Config { return c.Config_ }

// Reference return a channel reference for this channel
func (c *Channel) Reference() *assets.ChannelReference {
	if c == nil {
		return nil
	}
	return assets.NewChannelReference(c.UUID(), c.Name())
}

// GetChannelByID fetches a channel by ID even if it's deleted.
//
// NOTE that this function returns a "lite" channel with only sending related fields.
func GetChannelByID(ctx context.Context, db *sql.DB, id ChannelID) (*Channel, error) {
	row := db.QueryRowContext(ctx, sqlSelectChannelByID, id)
	ch := &Channel{}

	if err := dbutil.ScanJSON(row, ch); err != nil {
		return nil, fmt.Errorf("error fetching channel by id %d: %w", id, err)
	}

	return ch, nil
}

const sqlSelectChannelByID = `
SELECT ROW_TO_JSON(r) FROM (
    SELECT c.id, c.uuid, c.org_id, c.channel_type, c.name, c.address, COALESCE(c.tps, 10) AS tps, c.config
      FROM channels_channel c
     WHERE c.id = $1
) r;`

// AndroidGiveUpAge is how long an Android channel can go unseen before we give up on its relayer ever syncing:
// channels dark for longer stop getting FCM sync nudges, and outgoing messages older than this are failed rather
// than left in the outbox forever. The two have to move together - failing messages sooner than we stop nudging
// would permanently lose them if the relayer then came back, since only queued messages are offered to it.
const AndroidGiveUpAge = 72 * time.Hour

// GetAndroidChannelsToSync returns the android channels that have not synced between 15 min ago and AndroidGiveUpAge.
//
// NOTE that this function returns a "lite" channel with only sending related fields.
func GetAndroidChannelsToSync(ctx context.Context, db DBorTx) ([]Channel, error) {
	rows, err := db.QueryContext(ctx, sqlSelectAndroidChannelsToSync, time.Now().Add(-AndroidGiveUpAge))
	if err != nil {
		return nil, fmt.Errorf("error querying old seen android channels: %w", err)
	}

	return ScanJSONRows(rows, func() Channel { return Channel{} })
}

const sqlSelectAndroidChannelsToSync = `
SELECT ROW_TO_JSON(r) FROM (
    SELECT c.id, c.uuid, c.org_id, c.channel_type, c.name, c.address, COALESCE(c.tps, 10) AS tps, c.config
      FROM channels_channel c
     WHERE c.channel_type = 'A' AND c.last_seen >= $1 AND c.last_seen <  NOW() - INTERVAL '15 minutes' AND c.is_active = TRUE AND c.is_enabled = TRUE
  ORDER BY c.last_seen DESC, c.id DESC
) r;`

// AndroidChannel is the view of an Android channel needed to service a relayer sync. It's separate from the channel
// assets because a sync needs columns the assets don't carry (the shared secret, the claim code, last_seen) and needs
// them before we know which org's assets to load - and because a relayer can sync a channel that's been released or
// isn't claimed yet, neither of which appear in assets at all.
type AndroidChannel struct {
	ID        ChannelID          `json:"id"`
	UUID      assets.ChannelUUID `json:"uuid"`
	OrgID     OrgID              `json:"org_id"` // zero if the channel hasn't been claimed
	IsActive  bool               `json:"is_active"`
	Secret    string             `json:"secret"`
	ClaimCode string             `json:"claim_code"`
	Config    Config             `json:"config"`
	LastSeen  *time.Time         `json:"last_seen"`
	Device    string             `json:"device"`
	OS        string             `json:"os"`
}

const sqlSelectAndroidChannel = `
SELECT ROW_TO_JSON(r) FROM (
    SELECT c.id, c.uuid, c.org_id, c.is_active, c.secret, c.claim_code, c.config, c.last_seen, c.device, c.os
      FROM channels_channel c
     WHERE c.id = $1 AND c.channel_type = 'A'
) r;`

// GetAndroidChannel fetches the Android channel with the given id, including released and unclaimed ones. Returns
// ErrNotFound if there's no such channel, which for a syncing relayer means it should stop.
func GetAndroidChannel(ctx context.Context, db *sqlx.DB, id ChannelID) (*AndroidChannel, error) {
	row := db.QueryRowContext(ctx, sqlSelectAndroidChannel, id)
	ch := &AndroidChannel{}

	if err := dbutil.ScanJSON(row, ch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("error fetching android channel by id %d: %w", id, err)
	}

	return ch, nil
}

// UpdateAndroidChannelSeen records that the channel's relayer has just synced.
func UpdateAndroidChannelSeen(ctx context.Context, db DBorTx, id ChannelID) error {
	_, err := db.ExecContext(ctx, `UPDATE channels_channel SET last_seen = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("error updating channel last seen: %w", err)
	}
	return nil
}

// UpdateAndroidChannelApp records the FCM registration id and UUID a relayer reports for itself. The UUID is only
// written when the relayer actually sent one, because an older relayer omits it and mustn't be allowed to clear it.
func UpdateAndroidChannelApp(ctx context.Context, db DBorTx, id ChannelID, fcmID string, uuid assets.ChannelUUID) error {
	_, err := db.ExecContext(ctx,
		`UPDATE channels_channel SET config = config || jsonb_build_object($2::text, $3::text), uuid = COALESCE($4, uuid), modified_on = NOW() WHERE id = $1`,
		id, ChannelConfigFCMID, fcmID, null.String(uuid),
	)
	if err != nil {
		return fmt.Errorf("error updating channel app config: %w", err)
	}
	return nil
}

// UpdateAndroidChannelDevice records the device and OS a relayer reports for itself.
func UpdateAndroidChannelDevice(ctx context.Context, db DBorTx, id ChannelID, device, os string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE channels_channel SET device = $2, os = $3, modified_on = NOW() WHERE id = $1`, id, null.String(device), null.String(os),
	)
	if err != nil {
		return fmt.Errorf("error updating channel device: %w", err)
	}
	return nil
}

// ReleaseAndroidChannel deactivates a channel whose relayer has asked to be reset. It deliberately does less than
// releasing a channel from the UI does: it deactivates the channel and its triggers and ends its incidents here, and
// leaves interrupting sessions and failing queued messages to the interrupt channel task the caller queues. The rest
// of what the UI does either doesn't apply to Android channels (deactivating with a provider, template translations)
// or is recalculated anyway (flow dependency issues).
func ReleaseAndroidChannel(ctx context.Context, db *sqlx.DB, ch *AndroidChannel) error {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE channels_channel SET is_active = FALSE, modified_on = NOW() WHERE id = $1`, ch.ID); err != nil {
		return fmt.Errorf("error deactivating channel: %w", err)
	}

	// archive and release the channel's triggers, the same pair of changes the UI makes
	if _, err := tx.ExecContext(ctx,
		`UPDATE triggers_trigger SET is_archived = TRUE, is_active = FALSE, schedule_id = NULL, modified_on = NOW() WHERE channel_id = $1 AND is_active = TRUE`, ch.ID,
	); err != nil {
		return fmt.Errorf("error releasing channel triggers: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE notifications_incident SET ended_on = NOW() WHERE channel_id = $1 AND ended_on IS NULL`, ch.ID,
	); err != nil {
		return fmt.Errorf("error ending channel incidents: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error committing channel release: %w", err)
	}

	return nil
}

// loadChannels loads all the channels for the passed in org
func loadChannels(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.Channel, error) {
	rows, err := db.QueryContext(ctx, sqlSelectChannelsByOrg, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying channels for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.Channel { return &Channel{} })
}

const sqlSelectChannelsByOrg = `
SELECT ROW_TO_JSON(r) FROM (SELECT
      c.id,
      c.uuid,
      c.org_id,
      c.name,
      c.channel_type,
      COALESCE(c.tps, 10) AS tps,
      c.country,
      c.address,
      c.schemes,
      c.config,
      (SELECT ARRAY(SELECT CASE r WHEN 'R' THEN 'receive' WHEN 'S' THEN 'send' WHEN 'C' THEN 'call' WHEN 'A' THEN 'answer' END FROM unnest(regexp_split_to_array(c.role,'')) AS r)) AS roles,
      jsonb_extract_path(c.config, 'matching_prefixes') AS match_prefixes,
      jsonb_extract_path(c.config, 'allow_international') AS allow_international,
      jsonb_extract_path(c.config, 'machine_detection') AS machine_detection
    FROM channels_channel c
   WHERE c.org_id = $1 AND c.is_active = TRUE AND c.is_enabled = TRUE
ORDER BY c.created_on ASC
) r;`

// OrgIDForChannelUUID returns the org id for the passed in channel UUID if any
func OrgIDForChannelUUID(ctx context.Context, db DBorTx, channelUUID assets.ChannelUUID) (OrgID, error) {
	var orgID OrgID
	err := db.GetContext(ctx, &orgID, `SELECT org_id FROM channels_channel WHERE uuid = $1 AND is_active = TRUE`, channelUUID)
	if err != nil {
		return NilOrgID, fmt.Errorf("no channel found with uuid: %s: %w", channelUUID, err)
	}
	return orgID, nil
}

func (i *ChannelID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i ChannelID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *ChannelID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i ChannelID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
