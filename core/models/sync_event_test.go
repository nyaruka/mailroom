package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
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
