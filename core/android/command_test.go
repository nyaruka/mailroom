package android_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/android"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/stretchr/testify/assert"
)

func TestCommandParsing(t *testing.T) {
	// the app has sent numbers as numbers, floats and strings over the years, and unreadable values are left as
	// zero rather than failing the whole document
	tcs := []struct {
		json     string
		msgID    models.MsgID
		ts       time.Time
		powerLvl int
	}{
		{`{"cmd":"mt_sent","msg_id":123,"ts":1746361845000}`, 123, time.Date(2025, 5, 4, 12, 30, 45, 0, time.UTC), 0},
		{`{"cmd":"mt_sent","msg_id":"123","ts":1746361845000.0}`, 123, time.Date(2025, 5, 4, 12, 30, 45, 0, time.UTC), 0},
		{`{"cmd":"mt_sent","msg_id":"12.9","ts":"1746361845000"}`, 12, time.Date(2025, 5, 4, 12, 30, 45, 0, time.UTC), 0},
		{`{"cmd":"status","msg_id":"nonsense","ts":true,"p_lvl":80}`, 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 80},
		{`{"cmd":"status","p_lvl":"80"}`, 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 80},
		{`{"cmd":"status","power_level":55}`, 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 55},
		{`{"cmd":"status","p_lvl":99999999}`, 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), 100},
		{`{"cmd":"status","p_lvl":-5}`, 0, time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), -1},
	}

	for _, tc := range tcs {
		c := &android.Command{}
		assert.NoError(t, jsonx.Unmarshal([]byte(tc.json), c), "%s: unexpected parse error", tc.json)
		assert.Equal(t, tc.msgID, c.MessageID(), "%s: message id mismatch", tc.json)
		assert.Equal(t, tc.ts, c.Timestamp(), "%s: timestamp mismatch", tc.json)
		assert.Equal(t, tc.powerLvl, c.PowerLevel(), "%s: power level mismatch", tc.json)
	}

	// message ids an old app has truncated to negative are left to match nothing rather than remapped
	c := &android.Command{}
	assert.NoError(t, jsonx.Unmarshal([]byte(`{"cmd":"mt_sent","msg_id":-1902744384}`), c))
	assert.Equal(t, models.MsgID(-1902744384), c.MessageID())
}

func TestCommandFieldDialects(t *testing.T) {
	// which of the short and long names the device sent is what decides the value - a short name sent as empty
	// means empty, not "fall back to the long one"
	c := &android.Command{}
	assert.NoError(t, jsonx.Unmarshal([]byte(`{"cmd":"status","p_src":"","power_source":"BAT","net":"WIFI","pending":[],"pending_messages":[123],"retry_messages":[456]}`), c))
	assert.Equal(t, "", c.PowerSource())
	assert.Equal(t, "WIFI", c.NetworkType())
	assert.Equal(t, []models.MsgID{}, c.PendingMessages())
	assert.Equal(t, []models.MsgID{456}, c.RetryMessages())

	c = &android.Command{}
	assert.NoError(t, jsonx.Unmarshal([]byte(`{"cmd":"status","power_source":"USB","pending_messages":["12","34"]}`), c))
	assert.Equal(t, "USB", c.PowerSource())
	assert.Equal(t, []models.MsgID{12, 34}, c.PendingMessages())
}
