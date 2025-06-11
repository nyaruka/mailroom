package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/null/v3"
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
	ID_                 ChannelID               `json:"id"`
	UUID_               assets.ChannelUUID      `json:"uuid"`
	OrgID_              OrgID                   `json:"org_id"`
	Name_               string                  `json:"name"`
	Address_            string                  `json:"address"`
	Type_               ChannelType             `json:"channel_type"`
	TPS_                int                     `json:"tps"`
	Country_            null.String             `json:"country"`
	Schemes_            []string                `json:"schemes"`
	Roles_              []assets.ChannelRole    `json:"roles"`
	Features_           []assets.ChannelFeature `json:"features"`
	MatchPrefixes_      []string                `json:"match_prefixes"`
	AllowInternational_ bool                    `json:"allow_international"`
	MachineDetection_   bool                    `json:"machine_detection"`
	Config_             Config                  `json:"config"`
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

// Features returns the features this channel supports
func (c *Channel) Features() []assets.ChannelFeature { return c.Features_ }

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

// GetAndroidChannelsToSync returns the android channels that have not synced between 15 min ago up to 7 days.
//
// NOTE that this function returns a "lite" channel with only sending related fields.
func GetAndroidChannelsToSync(ctx context.Context, db DBorTx) ([]Channel, error) {
	rows, err := db.QueryContext(ctx, sqlSelectAndroidChannelsToSync)
	if err != nil {
		return nil, fmt.Errorf("error querying old seen android channels: %w", err)
	}

	return ScanJSONRows(rows, func() Channel { return Channel{} })
}

const sqlSelectAndroidChannelsToSync = `
SELECT ROW_TO_JSON(r) FROM (
    SELECT c.id, c.uuid, c.org_id, c.channel_type, c.name, c.address, COALESCE(c.tps, 10) AS tps, c.config
      FROM channels_channel c
     WHERE c.channel_type = 'A' AND c.last_seen >= NOW() - INTERVAL '7 days' AND c.last_seen <  NOW() - INTERVAL '15 minutes' AND c.is_active = TRUE AND c.is_enabled = TRUE
  ORDER BY c.last_seen DESC, c.id DESC
) r;`

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
      CASE WHEN channel_type IN ('FBA') THEN '{"optins"}'::text[] ELSE '{}'::text[] END AS features,
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
