// Package android implements the domain side of Android relayer channels: parsing the commands a relayer sends
// during a sync, applying them, and building the response that tells it what to do next. The endpoint that
// terminates relayer traffic lives in web/public and calls into here.
package android

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/mailroom/v26/core/models"
)

// what the columns a relayer's own report is written into will accept. Nothing stops a device sending us more than
// this, and a value the database rejects would fail that device's sync permanently, so they're clamped rather than
// allowed to reach the insert.
const (
	maxDeviceLen      = 255
	maxOSLen          = 255
	maxNetworkTypeLen = 128
	maxPowerSourceLen = 64
	maxPowerStatusLen = 64

	// the most ids we'll take from one sync's pending and retry lists, so that a device can't turn its own status
	// report into an enormous query
	maxReportedMsgIDs = 10000
)

// Command is one command in a relayer's sync payload. The app sends every command type through the same list, so
// this is the union of their fields - and because it's a frozen client that has spoken several dialects over the
// years, numbers are read leniently and the older long-form field names are still accepted.
type Command struct {
	Cmd string           `json:"cmd"`
	PID *json.RawMessage `json:"p_id"`

	// commands about a specific message
	MsgID flexInt `json:"msg_id"`
	TS    flexInt `json:"ts"`
	Msg   string  `json:"msg"`
	Phone string  `json:"phone"`

	// the call command
	Type     string  `json:"type"`
	Duration flexInt `json:"dur"`

	// the fcm command
	FCMID string `json:"fcm_id"`
	UUID  string `json:"uuid"`

	// the status command
	Dev             string     `json:"dev"`
	OSName          string     `json:"os"`
	AppVersion      string     `json:"app_version"`
	OrgID           *flexInt   `json:"org_id"`
	PSrc            *string    `json:"p_src"`
	PowerSourceLong *string    `json:"power_source"`
	PSts            *string    `json:"p_sts"`
	PowerStatusLong *string    `json:"power_status"`
	PLvl            *flexInt   `json:"p_lvl"`
	PowerLevelLong  *flexInt   `json:"power_level"`
	Net             *string    `json:"net"`
	NetworkTypeLong *string    `json:"network_type"`
	Pending         *[]flexInt `json:"pending"`
	PendingLong     *[]flexInt `json:"pending_messages"`
	Retry           *[]flexInt `json:"retry"`
	RetryLong       *[]flexInt `json:"retry_messages"`
}

// MessageID is the message this command is about.
//
// Note we deliberately do not try to undo the truncation an app older than v1.9.9 does to our ids - it held them in
// a signed 32 bit int, and the implementation this replaces mapped a negative id back by adding 2^32. That's only
// correct while a workspace's ids sit between 2^31 and 2^32. Below that nothing is ever truncated, and above it the
// app has lost high bits that no arithmetic recovers, so the mapping produces a valid id belonging to a different
// message - turning a harmless no-match into a status written against the wrong message. An id we can't make sense
// of is left to match nothing.
func (c *Command) MessageID() models.MsgID {
	return models.MsgID(c.MsgID)
}

// Timestamp is when the device says this command happened. It's in milliseconds, and truncated rather than rounded
// to stay byte-identical with what we've always recorded.
func (c *Command) Timestamp() time.Time {
	return time.Unix(int64(c.TS)/1000, 0).UTC()
}

func (c *Command) Device() string      { return c.Dev }
func (c *Command) OS() string          { return c.OSName }
func (c *Command) PowerSource() string { return derefStr(c.PSrc, c.PowerSourceLong) }
func (c *Command) PowerStatus() string { return derefStr(c.PSts, c.PowerStatusLong) }
func (c *Command) NetworkType() string { return derefStr(c.Net, c.NetworkTypeLong) }

// PowerLevel is the battery percentage the device reports, or -1 when it doesn't know. It's clamped because it goes
// into an integer column and is only ever meaningful as a percentage.
func (c *Command) PowerLevel() int {
	v := derefInt(c.PLvl, c.PowerLevelLong)
	if v == nil {
		return 0
	}
	return min(max(int(*v), -1), 100)
}

func (c *Command) PendingMessages() []models.MsgID { return msgIDs(c.Pending, c.PendingLong) }
func (c *Command) RetryMessages() []models.MsgID   { return msgIDs(c.Retry, c.RetryLong) }

// the device has used both a short and a long name for these fields over the years, and which one it sent is what
// decides the value - a short name sent as empty means empty, not "fall back to the long one"
func derefStr(short, long *string) string {
	if short != nil {
		return *short
	}
	if long != nil {
		return *long
	}
	return ""
}

func derefInt(short, long *flexInt) *flexInt {
	if short != nil {
		return short
	}
	return long
}

func msgIDs(short, long *[]flexInt) []models.MsgID {
	vals := short
	if vals == nil {
		vals = long
	}
	if vals == nil {
		return nil
	}

	capped := *vals
	if len(capped) > maxReportedMsgIDs {
		capped = capped[:maxReportedMsgIDs]
	}

	ids := make([]models.MsgID, len(capped))
	for i, v := range capped {
		ids[i] = models.MsgID(v)
	}
	return ids
}

// flexInt is an integer field from a relayer, which over the years has sent numbers as JSON numbers, as floats and
// as strings. It never reports an error: these all arrive in one document, so failing on a single odd value would
// reject the entire sync - and would do so for every sync that device ever sends, with no way to debug it in the
// field. Anything we can't read is left as zero, which is how the callers treat a field the device didn't send.
type flexInt int64

func (i *flexInt) UnmarshalJSON(b []byte) error {
	var raw any
	if err := jsonx.Unmarshal(b, &raw); err != nil {
		return nil
	}

	switch v := raw.(type) {
	case float64:
		*i = flexInt(int64(v))
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*i = flexInt(int64(f))
		}
	}

	return nil
}

// clean makes a string the device reported safe to store: valid UTF-8, no null bytes, and short enough for the
// column it's going into.
func clean(s string, limit int) string {
	return stringsx.Truncate(dbutil.ToValidUTF8(s), limit)
}
