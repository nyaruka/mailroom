package android

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/nyaruka/null/v3"
)

func init() {
	web.InternetRoute(http.MethodPost, "/relayer/sync/{id:[0-9]+}", handleRelayerSync)
	web.InternetRoute(http.MethodPost, "/relayer/sync/{id:[0-9]+}/", handleRelayerSync)
}

// how far out of step with us a relayer's clock is allowed to be before we reject its request
const relayerRequestMaxAge = 15 * time.Minute

// how long a relayer can go without syncing before we bump its last_seen again - it syncs far more often than this
// and every sync would otherwise be a write to the channel
const relayerSeenInterval = 5 * time.Minute

// the most we'll read from a relayer before authenticating it - the signature is over the whole body so we can't
// truncate, and a device has no reason to send us anything near this
const relayerMaxBodyBytes = 1 << 20

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

// the most messages we'll offer a relayer in a single sync. There's no limit on how many can be queued for it, but a
// relayer syncs continuously so a huge response just delays the first send - it'll come back for the rest.
const relayerOutboxLimit = 1000

// the channel event types a relayer can report - it only knows about calls, so anything else it sent would be a
// device we don't understand rather than something worth recording
var relayerEventTypes = map[string]models.ChannelEventType{
	"mo_call": models.EventTypeIncomingCall,
	"mo_miss": models.EventTypeMissedCall,
	"mt_call": models.EventTypeOutgoingCall,
	"mt_miss": models.EventTypeMissedOutgoing,
}

// error ids the relayer app knows, which are part of the wire protocol
const (
	errorIDSignature  = 1
	errorIDOldRequest = 3
	errorIDUnclaimed  = 4
)

// Handles a sync from an Android relayer. This is the app's only channel of communication with us, and the app is
// frozen, so both the request and the response are the protocol it has always spoken:
//
//	POST /relayer/sync/{id}?ts={epoch-seconds}&signature={sig}
//
//	{"cmds": [{"cmd": "fcm", "fcm_id": "1234", "uuid": "..."}, {"cmd": "mt_sent", "msg_id": 123, "ts": 1638000000000}]}
//
// where the signature is the URL-safe base64 of HMAC-SHA256 over the raw request body, keyed by the channel's secret
// concatenated with the ts query parameter. The response tells the relayer which of its commands we processed and
// what we want it to do next:
//
//	{"cmds": [{"cmd": "ack", "p_id": 1}, {"cmd": "mt_bcast", "to": [{"phone": "+593979...", "id": 123}], "msg": "hi"}]}
func handleRelayerSync(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
	relayerID, _ := strconv.Atoi(r.PathValue("id"))
	channelID := models.ChannelID(relayerID)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, relayerMaxBodyBytes))
	if err != nil {
		return fmt.Errorf("error reading request body: %w", err)
	}

	channel, err := models.GetAndroidChannel(ctx, rt.DB, channelID)
	if err != nil && !errors.Is(err, models.ErrNotFound) {
		return fmt.Errorf("error loading channel: %w", err)
	}

	// a channel we don't have, or that's been released, tells its relayer to stop
	if channel == nil || !channel.IsActive {
		return writeSyncResponse(w, []any{map[string]any{"cmd": "rel", "relayer_id": relayerID}})
	}

	// a channel without a secret was never claimed and can't have signed anything
	if channel.Secret == "" {
		return writeSyncError(w, errorIDUnclaimed, "Can't sync unclaimed channel")
	}

	requestTS := r.URL.Query().Get("ts")
	ts, err := strconv.ParseInt(requestTS, 10, 64)
	if err != nil {
		return fmt.Errorf("unreadable ts on relayer sync: %s", requestTS)
	}
	if absDuration(time.Since(time.Unix(ts, 0))) > relayerRequestMaxAge {
		return writeSyncError(w, errorIDOldRequest, "Old Request")
	}

	if !checkRelayerSignature(channel.Secret, requestTS, body, r.URL.Query().Get("signature")) {
		return writeSyncError(w, errorIDSignature, "Invalid signature")
	}

	if channel.LastSeen == nil || time.Since(*channel.LastSeen) > relayerSeenInterval {
		if err := models.UpdateAndroidChannelSeen(ctx, rt.DB, channel.ID); err != nil {
			return err
		}
	}

	var cmds []*syncCommand

	if len(body) > 0 {
		payload := &struct {
			Commands []*syncCommand `json:"cmds"`
		}{}
		if err := json.Unmarshal(body, payload); err != nil {
			return fmt.Errorf("unparseable relayer sync body: %w", err)
		}

		// every valid sync starts by telling us how to reach the device
		if len(payload.Commands) < 1 || payload.Commands[0].Command != "fcm" {
			return writeSyncError(w, errorIDUnclaimed, "Missing FCM command")
		}

		cmds = payload.Commands
	}

	// a channel that hasn't been claimed yet gets sent its claim code so the device can display it
	if channel.OrgID == models.NilOrgID {
		if len(cmds) > 0 && cmds[0].UUID == string(channel.UUID) {
			return writeSyncResponse(w, []any{map[string]any{
				"cmd":                "reg",
				"relayer_claim_code": channel.ClaimCode,
				"relayer_secret":     channel.Secret,
				"relayer_id":         relayerID,
			}})
		}
		return writeSyncError(w, errorIDUnclaimed, "Can't sync unclaimed channel")
	}

	return processRelayerSync(ctx, rt, w, channel, cmds)
}

func processRelayerSync(ctx context.Context, rt *runtime.Runtime, w http.ResponseWriter, channel *models.AndroidChannel, cmds []*syncCommand) error {
	oa, err := models.GetOrgAssets(ctx, rt, channel.OrgID)
	if err != nil {
		return fmt.Errorf("error loading org assets: %w", err)
	}

	// resolve the ids of every message the relayer has something to say about in one go
	msgIDs := make([]models.MsgID, 0, len(cmds))
	for _, c := range cmds {
		if c.MsgID != 0 {
			msgIDs = append(msgIDs, c.MessageID())
		}
	}
	refs, err := getMessageRefs(ctx, rt, channel.OrgID, msgIDs)
	if err != nil {
		return fmt.Errorf("error resolving messages to update: %w", err)
	}

	resp := make([]any, 0, len(cmds))
	batch := &statusBatch{}
	seenCalls := make(map[string]bool, len(cmds))
	var syncEvent *models.SyncEvent
	var outboxExclude []models.MsgID

	// a command failing partway through mustn't discard the status changes we've already accepted, so the loop
	// records the failure and breaks rather than returning - the batch is applied either way below
	var loopErr error

	for _, c := range cmds {
		if c.Command == "" {
			continue
		}

		handled := false
		var extra map[string]any

		switch {
		// any command carrying a message id is about that message, whatever else it says
		case c.MsgID != 0:
			handled = batch.add(refs[c.MessageID()], c.Command, c.Timestamp())

		case c.Command == "mo_sms":
			handled = true

			if c.Phone != "" && c.Msg != "" {
				msgID, _, err := createIncomingMsg(ctx, rt, oa, channel.ID, c.Phone, c.Msg, c.Timestamp())
				if err != nil {
					// a phone number we can't parse isn't something the relayer can fix by sending it again
					if !isInvalidURN(err) {
						loopErr = fmt.Errorf("error creating incoming message: %w", err)
					}
				} else {
					extra = map[string]any{"msg_id": msgID}
				}
			}

		case c.Command == "call":
			handled = true

			// the device sometimes reports calls from an 'unknown number', which we have nothing to attach to a
			// contact, and it repeats the same call across commands in one sync
			call := fmt.Sprintf("%d|%s|%s", c.TS, c.Type, c.Phone)
			eventType, validType := relayerEventTypes[c.Type]

			if c.Phone != "" && validType && !seenCalls[call] {
				_, err := createChannelEvent(ctx, rt, oa, channel.ID, c.Phone, eventType, null.Map[any]{"duration": int(c.Duration)}, c.Timestamp())
				if err != nil && !isInvalidURN(err) {
					loopErr = fmt.Errorf("error creating channel event: %w", err)
				}
				seenCalls[call] = true
			}

		case c.Command == "fcm":
			// this is how we reach the device to ask it to sync, so it's never acked - the relayer includes it in
			// every sync and we want the latest
			if err := updateChannelApp(ctx, rt, channel, c); err != nil {
				loopErr = err
			}

		case c.Command == "reset":
			if err := releaseChannel(ctx, rt, channel); err != nil {
				loopErr = err
			} else {
				handled = true
			}

		case c.Command == "status":
			// the device's own report of itself, always included in a sync so never acked
			syncEvent, err = recordSyncEvent(ctx, rt, oa, channel, c, len(cmds))
			if err != nil {
				loopErr = err
				break
			}
			outboxExclude = append(c.PendingMessages(), c.RetryMessages()...)

			// the channel has been moved to a different workspace than the device thinks it's in
			if c.OrgID != nil && models.OrgID(*c.OrgID) != channel.OrgID {
				resp = append(resp, map[string]any{"cmd": "claim", "org_id": channel.OrgID})
			}
		}

		if loopErr != nil {
			break
		}

		if c.PID != nil && handled {
			// echo the id back exactly as it was sent rather than re-encoding it, since the device matches its
			// pending commands against what it gave us
			ack := map[string]any{"cmd": "ack", "p_id": *c.PID}
			if extra != nil {
				ack["extra"] = extra
			}
			resp = append(resp, ack)
		}
	}

	if err := batch.apply(ctx, rt, channel.OrgID); err != nil {
		return err
	}
	if loopErr != nil {
		return loopErr
	}

	outbox, err := buildOutboxCommands(ctx, rt, channel.ID, outboxExclude)
	if err != nil {
		return err
	}
	resp = append(resp, outbox...)

	if syncEvent != nil {
		// acks aren't work we did for the device, so they don't count as commands we sent it
		if err := syncEvent.UpdateOutgoingCommandCount(ctx, rt.DB, len(resp)-countAcks(resp)); err != nil {
			return err
		}
	}

	return writeSyncResponse(w, resp)
}

// updateChannelApp records the FCM id and UUID the device reports for itself, so that we can reach it to trigger a
// sync and so a re-installed app can re-attach itself to this channel.
func updateChannelApp(ctx context.Context, rt *runtime.Runtime, channel *models.AndroidChannel, c *syncCommand) error {
	// the column is a real uuid type, so anything else would be rejected by the database and take every later sync
	// from this device down with it - we'd rather carry on with the uuid we already have
	uuid := assets.ChannelUUID(c.UUID)
	if uuid != "" && !uuids.Is(string(uuid)) {
		slog.Warn("ignoring unusable uuid from relayer", "channel_id", channel.ID, "uuid", uuid)
		uuid = ""
	}

	if channel.Config.GetString(models.ChannelConfigFCMID, "") == c.FCMID && (uuid == "" || uuid == channel.UUID) {
		return nil
	}

	if err := models.UpdateAndroidChannelApp(ctx, rt.DB, channel.ID, c.FCMID, uuid); err != nil {
		return err
	}

	// the next FCM nudge has to use the registration id we were just given
	if _, err := models.GetOrgAssetsWithRefresh(ctx, rt, channel.OrgID, models.RefreshChannels); err != nil {
		return fmt.Errorf("error refreshing channels: %w", err)
	}

	return nil
}

// recordSyncEvent stores what the device reported about itself, and raises or clears the incident for a device
// running an app older than the one we're distributing.
func recordSyncEvent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.AndroidChannel, c *syncCommand, numCmds int) (*models.SyncEvent, error) {
	device, os := clean(c.Device(), maxDeviceLen), clean(c.OS(), maxOSLen)
	if device != channel.Device || os != channel.OS {
		if err := models.UpdateAndroidChannelDevice(ctx, rt.DB, channel.ID, device, os); err != nil {
			return nil, err
		}
	}

	e := &models.SyncEvent{
		ChannelID:           channel.ID,
		PowerSource:         clean(c.PowerSource(), maxPowerSourceLen),
		PowerStatus:         clean(c.PowerStatus(), maxPowerStatusLen),
		PowerLevel:          c.PowerLevel(),
		NetworkType:         clean(c.NetworkType(), maxNetworkTypeLen),
		PendingMessageCount: len(c.PendingMessages()),
		RetryMessageCount:   len(c.RetryMessages()),

		// the fcm and status commands are in every sync, so they aren't work the device is reporting
		IncomingCommandCount: max(numCmds-2, 0),
	}
	if err := e.Insert(ctx, rt.DB); err != nil {
		return nil, err
	}

	if err := checkAppVersion(ctx, rt, oa, channel, c.AppVersion); err != nil {
		return nil, err
	}

	return e, nil
}

func checkAppVersion(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, channel *models.AndroidChannel, reported string) error {
	latest, err := models.GetLatestAndroidAppVersion(ctx, rt.DB)
	if err != nil {
		return err
	}

	// we can only judge the device's version if it told us one and we have something to compare it to
	if latest == "" || reported == "" {
		return nil
	}

	if reported != latest {
		_, notifications, err := models.IncidentChannelOutdatedApp(ctx, rt.DB, oa, channel.ID)
		if err != nil {
			return fmt.Errorf("error creating outdated app incident: %w", err)
		}
		if err := models.PublishNotifications(ctx, rt, oa, notifications); err != nil {
			return fmt.Errorf("error publishing incident notifications: %w", err)
		}
	} else if err := models.EndChannelOutdatedAppIncidents(ctx, rt.DB, channel.ID); err != nil {
		return err
	}

	return nil
}

// releaseChannel handles a device asking to be reset, i.e. disconnected from this channel.
func releaseChannel(ctx context.Context, rt *runtime.Runtime, channel *models.AndroidChannel) error {
	if err := models.ReleaseAndroidChannel(ctx, rt.DB, channel); err != nil {
		return err
	}

	// the channel is no longer somewhere messages can be sent from
	if _, err := models.GetOrgAssetsWithRefresh(ctx, rt, channel.OrgID, models.RefreshChannels); err != nil {
		return fmt.Errorf("error refreshing channels: %w", err)
	}

	// interrupting sessions and failing whatever is queued for the channel is its own job
	task := &tasks.InterruptChannel{ChannelID: channel.ID}
	if err := tasks.Queue(ctx, rt, rt.Queues.Batch, channel.OrgID, task, true); err != nil {
		return fmt.Errorf("error queuing interrupt channel task: %w", err)
	}

	return nil
}

// buildOutboxCommands turns the messages waiting for this channel into the fewest send commands that describe them,
// by grouping consecutive messages with the same text into one broadcast.
func buildOutboxCommands(ctx context.Context, rt *runtime.Runtime, channelID models.ChannelID, exclude []models.MsgID) ([]any, error) {
	msgs, err := models.GetAndroidOutbox(ctx, rt.DB, channelID, exclude, relayerOutboxLimit)
	if err != nil {
		return nil, err
	}

	cmds := make([]any, 0, 10)
	var to []map[string]any
	text := ""

	for _, m := range msgs {
		if m.Text != text && len(to) > 0 {
			cmds = append(cmds, map[string]any{"cmd": "mt_bcast", "to": to, "msg": text})
			to = nil
		}

		text = m.Text
		to = append(to, map[string]any{"phone": m.Phone, "id": m.ID})
	}

	if len(to) > 0 {
		cmds = append(cmds, map[string]any{"cmd": "mt_bcast", "to": to, "msg": text})
	}

	return cmds, nil
}

// checkRelayerSignature verifies that a request really came from the device holding the channel's secret.
func checkRelayerSignature(secret, ts string, body []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(secret+ts))
	mac.Write(body)
	expected := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

func writeSyncResponse(w http.ResponseWriter, cmds []any) error {
	if cmds == nil {
		cmds = []any{}
	}
	return web.WriteMarshalled(w, http.StatusOK, map[string]any{"cmds": cmds})
}

func writeSyncError(w http.ResponseWriter, errorID int, message string) error {
	slog.Debug("relayer sync rejected", "error_id", errorID, "error", message)

	return web.WriteMarshalled(w, http.StatusUnauthorized, map[string]any{"error_id": errorID, "error": message, "cmds": []any{}})
}

func countAcks(cmds []any) int {
	count := 0
	for _, c := range cmds {
		if m, ok := c.(map[string]any); ok && m["cmd"] == "ack" {
			count++
		}
	}
	return count
}

// isInvalidURN reports whether the error is the device having given us a phone number we can't make sense of.
func isInvalidURN(err error) bool {
	var urnErr *models.URNError
	return errors.As(err, &urnErr) && urnErr.Code == "invalid"
}

// clean makes a string the device reported safe to store: valid UTF-8, no null bytes, and short enough for the
// column it's going into.
func clean(s string, limit int) string {
	return stringsx.Truncate(dbutil.ToValidUTF8(s), limit)
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// syncCommand is one command in a relayer's sync payload. The app sends every command type through the same list, so
// this is the union of their fields - and because it's a frozen client that has spoken several dialects over the
// years, numbers are read leniently and the older long-form field names are still accepted.
type syncCommand struct {
	Command string           `json:"cmd"`
	PID     *json.RawMessage `json:"p_id"`

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
func (c *syncCommand) MessageID() models.MsgID {
	return msgID(c.MsgID)
}

// msgID reads a message id as reported by a relayer. Our ids are 64 bit, but the app held them in a signed 32 bit int
// until v1.9.9 - it parsed the id we send it with getInt() - so an id past that range comes back from an older device
// as a negative number and has to be reinterpreted as unsigned. This only recovers ids below 2^32; past that the app
// has lost bits we can't reconstruct.
//
// The implementation this replaces only did this for the id a status command is about, and compared the pending and
// retry lists raw. Those carry the same truncated ids, so an older device was re-offered messages it had already told
// us it holds, and sent them twice. Applying it to every id is a deliberate difference that fixes that: ids are never
// legitimately negative, so it's inert for any device on v1.9.9 or later.
func msgID(v flexInt) models.MsgID {
	id := int64(v)
	if id < 0 {
		id += 1 << 32
	}
	return models.MsgID(id)
}

// Timestamp is when the device says this command happened. It's in milliseconds, and truncated rather than rounded
// to stay byte-identical with what we've always recorded.
func (c *syncCommand) Timestamp() time.Time {
	return time.Unix(int64(c.TS)/1000, 0).UTC()
}

func (c *syncCommand) Device() string      { return c.Dev }
func (c *syncCommand) OS() string          { return c.OSName }
func (c *syncCommand) PowerSource() string { return derefStr(c.PSrc, c.PowerSourceLong) }
func (c *syncCommand) PowerStatus() string { return derefStr(c.PSts, c.PowerStatusLong) }
func (c *syncCommand) NetworkType() string { return derefStr(c.Net, c.NetworkTypeLong) }

// PowerLevel is the battery percentage the device reports, or -1 when it doesn't know. It's clamped because it goes
// into an integer column and is only ever meaningful as a percentage.
func (c *syncCommand) PowerLevel() int {
	v := derefInt(c.PLvl, c.PowerLevelLong)
	if v == nil {
		return 0
	}
	return min(max(int(*v), -1), 100)
}

func (c *syncCommand) PendingMessages() []models.MsgID { return msgIDs(c.Pending, c.PendingLong) }
func (c *syncCommand) RetryMessages() []models.MsgID   { return msgIDs(c.Retry, c.RetryLong) }

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
		ids[i] = msgID(v)
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
