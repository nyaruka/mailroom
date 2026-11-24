package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncEvent(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	e := &models.SyncEvent{
		ChannelID:            testdb.AndroidChannel.ID,
		PowerSource:          models.SyncSourceBattery,
		PowerStatus:          models.SyncStatusDischarging,
		PowerLevel:           64,
		NetworkType:          "WIFI",
		PendingMessageCount:  3,
		RetryMessageCount:    1,
		IncomingCommandCount: 5,
	}

	assert.NoError(t, e.Insert(ctx, rt.DB))
	assert.NotEqual(t, models.NilSyncEventID, e.ID)

	assertdb.Query(t, rt.DB, `SELECT power_source, power_status, power_level, network_type, pending_message_count, retry_message_count, incoming_command_count, outgoing_command_count FROM channels_syncevent WHERE id = $1`, e.ID).
		Columns(map[string]any{
			"power_source": "BAT", "power_status": "DIS", "power_level": 64, "network_type": "WIFI",
			"pending_message_count": 3, "retry_message_count": 1, "incoming_command_count": 5, "outgoing_command_count": 0,
		})

	// how many commands we sent back isn't known until the rest of the sync has been processed
	assert.NoError(t, e.UpdateOutgoingCommandCount(ctx, rt.DB, 7))
	assert.Equal(t, 7, e.OutgoingCommandCount)

	assertdb.Query(t, rt.DB, `SELECT outgoing_command_count FROM channels_syncevent WHERE id = $1`, e.ID).Returns(7)

	// the channel's latest sync is merged in alongside any other kind of sync's state
	rt.DB.MustExec(`UPDATE channels_channel SET sync_state = '{"templates": {"failures": 2}}' WHERE id = $1`, testdb.AndroidChannel.ID)

	assert.NoError(t, e.UpdateChannelSyncState(ctx, rt.DB))

	assertdb.Query(t, rt.DB, `SELECT (sync_state->'templates')::text FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns(`{"failures": 2}`)
	assertdb.Query(t, rt.DB, `SELECT ((sync_state->'android') - 'synced_on')::text FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).
		Returns(`{"power_level": 64, "network_type": "WIFI", "power_source": "BAT", "power_status": "DIS", "retry_message_count": 1, "pending_message_count": 3, "incoming_command_count": 5, "outgoing_command_count": 7}`)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channel WHERE id = $1 AND (sync_state->'android'->>'synced_on')::timestamptz > NOW() - INTERVAL '1 minute'`, testdb.AndroidChannel.ID).Returns(1)

	// a later sync replaces it
	e.PowerLevel = 50
	assert.NoError(t, e.UpdateChannelSyncState(ctx, rt.DB))

	assertdb.Query(t, rt.DB, `SELECT sync_state->'android'->>'power_level' FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns(`50`)
	assertdb.Query(t, rt.DB, `SELECT (sync_state->'templates')::text FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns(`{"failures": 2}`)
}

func TestSyncEventDynamo(t *testing.T) {
	syncedOn := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
	e := &models.SyncEvent{
		UUID:                 "019ab5e3-4f2a-7c00-8d00-123456789abc",
		OrgID:                testdb.Org1.ID,
		ChannelID:            testdb.AndroidChannel.ID,
		ChannelUUID:          testdb.AndroidChannel.UUID,
		SyncedOn:             syncedOn,
		PowerSource:          models.SyncSourceAC,
		PowerStatus:          models.SyncStatusCharging,
		PowerLevel:           90,
		NetworkType:          "MOBILE",
		PendingMessageCount:  2,
		RetryMessageCount:    1,
		IncomingCommandCount: 4,
		OutgoingCommandCount: 3,
	}

	item, err := e.MarshalDynamo()
	require.NoError(t, err)

	ttl := syncedOn.Add(7 * 24 * time.Hour)
	assert.Equal(t, &dynamo.Item{
		Key:   dynamo.Key{PK: "chs#" + string(testdb.AndroidChannel.UUID), SK: "syn#019ab5e3-4f2a-7c00-8d00-123456789abc"},
		OrgID: int(testdb.Org1.ID),
		TTL:   &ttl,
		Data: map[string]any{
			"synced_on":              syncedOn,
			"power_source":           "AC",
			"power_status":           "CHA",
			"power_level":            90,
			"network_type":           "MOBILE",
			"pending_message_count":  2,
			"retry_message_count":    1,
			"incoming_command_count": 4,
			"outgoing_command_count": 3,
		},
	}, item)
}
