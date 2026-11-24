package models

import (
	"context"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
)

// how long a channel's sync history is kept, which only needs to be long enough to see how its phone has been doing
const androidSyncDynamoTTL = time.Hour * 24 * 7

// power source values reported by a relayer
const (
	SyncSourceAC       = "AC"
	SyncSourceUSB      = "USB"
	SyncSourceWireless = "WIR"
	SyncSourceBattery  = "BAT"
)

// power status values reported by a relayer
const (
	SyncStatusUnknown     = "UNK"
	SyncStatusCharging    = "CHA"
	SyncStatusDischarging = "DIS"
	SyncStatusNotCharging = "NOT"
	SyncStatusFull        = "FUL"
)

// AndroidSync is a single sync by an Android channel's relayer, holding what the device reported about itself and how
// much work the sync did.
type AndroidSync struct {
	UUID                 uuids.UUID
	OrgID                OrgID
	ChannelID            ChannelID
	ChannelUUID          assets.ChannelUUID
	SyncedOn             time.Time
	PowerSource          string
	PowerStatus          string
	PowerLevel           int
	NetworkType          string
	PendingMessageCount  int
	RetryMessageCount    int
	IncomingCommandCount int
	OutgoingCommandCount int
}

// merged in under its own key so it never clobbers the state of another kind of sync
const sqlUpdateAndroidSyncState = `
UPDATE channels_channel
   SET sync_state = COALESCE(sync_state, '{}') || jsonb_build_object('android', jsonb_build_object(
           'synced_on', NOW(),
           'power_source', $2::text,
           'power_status', $3::text,
           'power_level', $4::int,
           'network_type', $5::text,
           'pending_message_count', $6::int,
           'retry_message_count', $7::int,
           'incoming_command_count', $8::int,
           'outgoing_command_count', $9::int
       ))
 WHERE id = $1`

// UpdateChannelSyncState records this sync as its channel's latest Android sync.
func (s *AndroidSync) UpdateChannelSyncState(ctx context.Context, db DBorTx) error {
	_, err := db.ExecContext(ctx, sqlUpdateAndroidSyncState, s.ChannelID, s.PowerSource, s.PowerStatus, s.PowerLevel,
		s.NetworkType, s.PendingMessageCount, s.RetryMessageCount, s.IncomingCommandCount, s.OutgoingCommandCount)
	if err != nil {
		return fmt.Errorf("error updating channel android sync state: %w", err)
	}
	return nil
}

// AndroidSyncDynamoKey returns the DynamoDB key of a sync in its channel's history, which sorts by time since the UUID
// is a V7.
func AndroidSyncDynamoKey(channelUUID assets.ChannelUUID, uuid uuids.UUID) dynamo.Key {
	return dynamo.Key{PK: fmt.Sprintf("cha#%s", channelUUID), SK: fmt.Sprintf("syn#%s", uuid)}
}

func (s *AndroidSync) DynamoKey() dynamo.Key {
	return AndroidSyncDynamoKey(s.ChannelUUID, s.UUID)
}

// MarshalDynamo marshals this sync as an item in its channel's history, with the same values as its sync state.
func (s *AndroidSync) MarshalDynamo() (*dynamo.Item, error) {
	ttl := s.SyncedOn.Add(androidSyncDynamoTTL)

	return &dynamo.Item{
		Key:   s.DynamoKey(),
		OrgID: int(s.OrgID),
		TTL:   &ttl,
		Data: map[string]any{
			"synced_on":              s.SyncedOn,
			"power_source":           s.PowerSource,
			"power_status":           s.PowerStatus,
			"power_level":            s.PowerLevel,
			"network_type":           s.NetworkType,
			"pending_message_count":  s.PendingMessageCount,
			"retry_message_count":    s.RetryMessageCount,
			"incoming_command_count": s.IncomingCommandCount,
			"outgoing_command_count": s.OutgoingCommandCount,
		},
	}, nil
}
