package android_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const relayerSecret = "0123456789abcdef0123456789abcdef"

// syncs the given channel the way a relayer does, signing the body with the given secret
func relayerSync(t *testing.T, rt *runtime.Runtime, channelID models.ChannelID, secret, body string, tweak func(q *relayerQuery)) (int, map[string]any) {
	q := &relayerQuery{ts: fmt.Sprintf("%d", time.Now().Unix())}

	mac := hmac.New(sha256.New, []byte(secret+q.ts))
	mac.Write([]byte(body))
	q.signature = base64.URLEncoding.EncodeToString(mac.Sum(nil))

	if tweak != nil {
		tweak(q)
	}

	url := fmt.Sprintf("http://localhost:%d/mr/relayer/sync/%d%s?ts=%s&signature=%s", rt.Config.InternetPort, channelID, q.suffix, q.ts, q.signature)
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	parsed := map[string]any{}
	require.NoError(t, jsonx.Unmarshal(respBody, &parsed), "unparseable response: %s", respBody)

	return resp.StatusCode, parsed
}

type relayerQuery struct {
	ts        string
	signature string
	suffix    string
}

// starts a web server for the duration of the test
func startServer(t *testing.T, rt *runtime.Runtime) {
	wg := &sync.WaitGroup{}
	server := web.NewServer(t.Context(), rt, wg)
	server.Start()
	t.Cleanup(server.Stop)

	time.Sleep(100 * time.Millisecond) // give server time to start
}

// makes the standard test android channel claimable by a relayer
func claimAndroidChannel(t *testing.T, rt *runtime.Runtime) {
	rt.DB.MustExec(`UPDATE channels_channel SET secret = $2, claim_code = 'CLAIM123', last_seen = NULL WHERE id = $1`, testdb.AndroidChannel.ID, relayerSecret)
	models.FlushCache()
}

func TestRelayerSyncAuth(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	fcm := `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123","p_id":1}]}`

	// a channel we don't have tells its relayer to stop
	status, resp := relayerSync(t, rt, 123456, relayerSecret, fcm, nil)
	assert.Equal(t, 200, status)
	test.AssertEqualJSON(t, []byte(`{"cmds":[{"cmd":"rel","relayer_id":123456}]}`), jsonx.MustMarshal(resp), "unexpected response for unknown channel")

	// as does one that's been released
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.TwilioChannel.ID)
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.AndroidChannel.ID)
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fcm, nil)
	assert.Equal(t, 200, status)
	assert.Equal(t, "rel", resp["cmds"].([]any)[0].(map[string]any)["cmd"])
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = TRUE WHERE id = $1`, testdb.AndroidChannel.ID)

	// a signature over a different body is rejected
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, "wrong-secret", fcm, nil)
	assert.Equal(t, 401, status)
	assert.Equal(t, float64(1), resp["error_id"])
	assert.Equal(t, "Invalid signature", resp["error"])

	// as is a request whose clock is more than 15 minutes out
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fcm, func(q *relayerQuery) {
		q.ts = fmt.Sprintf("%d", time.Now().Add(-30*time.Minute).Unix())
	})
	assert.Equal(t, 401, status)
	assert.Equal(t, float64(3), resp["error_id"])
	assert.Equal(t, "Old Request", resp["error"])

	// a request with no readable ts at all is broken rather than merely old
	status, _ = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fcm, func(q *relayerQuery) { q.ts = "" })
	assert.Equal(t, 500, status)

	// a body that doesn't start with an fcm command is rejected
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, `{"cmds":[{"cmd":"status","p_lvl":80}]}`, nil)
	assert.Equal(t, 401, status)
	assert.Equal(t, float64(4), resp["error_id"])

	// a valid sync is accepted, and records that we've seen the device
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fcm, nil)
	assert.Equal(t, 200, status)
	assert.Equal(t, []any{}, resp["cmds"])

	// the app's own URL has a trailing slash, so that has to work too
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fcm, func(q *relayerQuery) { q.suffix = "/" })
	assert.Equal(t, 200, status)
	assert.Equal(t, []any{}, resp["cmds"])

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channel WHERE id = $1 AND last_seen > NOW() - INTERVAL '1 minute'`, testdb.AndroidChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT config->>'FCM_ID' FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns("FCM123")
}

func TestRelayerSyncUnclaimed(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)

	ch := testdb.InsertChannel(t, rt, testdb.Org1, "A", "Unclaimed", "", []string{"tel"}, "SR", map[string]any{})
	rt.DB.MustExec(`UPDATE channels_channel SET org_id = NULL, secret = $2, claim_code = 'CLAIM123' WHERE id = $1`, ch.ID, relayerSecret)

	// a device syncing a channel that hasn't been claimed gets sent the claim code to display
	body := fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123","uuid":"%s"}]}`, ch.UUID)
	status, resp := relayerSync(t, rt, ch.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	cmds := resp["cmds"].([]any)
	require.Len(t, cmds, 1)
	assert.Equal(t, "reg", cmds[0].(map[string]any)["cmd"])
	assert.Equal(t, "CLAIM123", cmds[0].(map[string]any)["relayer_claim_code"])
	assert.Equal(t, relayerSecret, cmds[0].(map[string]any)["relayer_secret"])

	// but a device reporting some other channel's uuid is told it can't sync
	status, resp = relayerSync(t, rt, ch.ID, relayerSecret, `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123","uuid":"a1b2c3d4-e5f6-4a5b-8c9d-0e1f2a3b4c5d"}]}`, nil)
	assert.Equal(t, 401, status)
	assert.Equal(t, float64(4), resp["error_id"])

	// a channel that never got a secret can't have signed anything
	rt.DB.MustExec(`UPDATE channels_channel SET secret = NULL WHERE id = $1`, ch.ID)
	status, resp = relayerSync(t, rt, ch.ID, relayerSecret, body, nil)
	assert.Equal(t, 401, status)
	assert.Equal(t, "Can't sync unclaimed channel", resp["error"])
}

func TestRelayerSyncCommands(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)
	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.AndroidChannel, testdb.Bob, "hi", nil, models.MsgStatusQueued, false)
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.AndroidChannel, testdb.Cat, "hi", nil, models.MsgStatusQueued, false)

	body := fmt.Sprintf(`{"cmds":[
		{"cmd":"fcm","fcm_id":"FCM456","p_id":1},
		{"cmd":"mo_sms","phone":"+593979000111","msg":"incoming!","ts":1746361845000,"p_id":2},
		{"cmd":"call","phone":"+593979000111","type":"mo_miss","ts":1746361846000,"dur":0,"p_id":3},
		{"cmd":"mt_sent","msg_id":%d,"ts":1746361847000,"p_id":4},
		{"cmd":"mt_dlvd","msg_id":%d,"ts":1746361848000,"p_id":5},
		{"cmd":"mt_error","msg_id":%d,"ts":1746361849000,"p_id":6},
		{"cmd":"mt_sent","msg_id":987654,"ts":1746361850000,"p_id":7},
		{"cmd":"unknown","p_id":8},
		{"cmd":"mt_fail","msg_id":%d,"ts":1746361851000,"p_id":0},
		{"cmd":"mt_fail","msg_id":%d,"ts":1746361852000,"p_id":10},
		{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[],"retry":[],"dev":"Nexus","os":"7.0","app_version":"1.2.3"}
	]}`, out1.ID, out1.ID, out2.ID, out3.ID, int64(out3.ID)-(1<<32))

	status, resp := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	cmds := resp["cmds"].([]any)
	acked := []float64{}
	for _, c := range cmds {
		if m := c.(map[string]any); m["cmd"] == "ack" {
			acked = append(acked, m["p_id"].(float64))
		}
	}

	// fcm and status are never acked, and the unknown command, the one for a message we don't have and the one
	// naming a truncated negative id all go unhandled. p_id 0 is a real id the device is waiting on, so it's acked.
	assert.Equal(t, []float64{2, 3, 4, 5, 6, 0}, acked)

	// the incoming message was created and its id reported back so the device can match it up
	var moAck map[string]any
	for _, c := range cmds {
		if m := c.(map[string]any); m["cmd"] == "ack" && m["p_id"].(float64) == 2 {
			moAck = m
		}
	}
	require.NotNil(t, moAck)
	assert.NotNil(t, moAck["extra"].(map[string]any)["msg_id"])

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE direction = 'I' AND text = 'incoming!' AND channel_id = $1`, testdb.AndroidChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channelevent WHERE event_type = 'mo_miss' AND channel_id = $1`, testdb.AndroidChannel.ID).Returns(1)

	// sent then delivered for the same message folds into one update keeping the sent timestamp
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out1.ID).
		Columns(map[string]any{"status": "D", "folder": "S"})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE id = $1 AND sent_on = '2025-05-04T12:30:47Z'`, out1.ID).Returns(1)

	// the errored message stays in the outbox, so it's offered back to the relayer as well
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out2.ID).
		Columns(map[string]any{"status": "E", "folder": "O"})

	// out3 was failed by the command naming it properly; the one naming it as a truncated negative matched nothing
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out3.ID).
		Columns(map[string]any{"status": "F", "folder": "X"})

	// the device's own report was recorded
	assertdb.Query(t, rt.DB, `SELECT power_source, power_status, power_level, network_type, incoming_command_count FROM channels_syncevent WHERE channel_id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"power_source": "BAT", "power_status": "CHA", "power_level": 80, "network_type": "WIFI", "incoming_command_count": 9})
	assertdb.Query(t, rt.DB, `SELECT device, os FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"device": "Nexus", "os": "7.0"})

	// the fcm id it reported is what we'll use to nudge it
	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)
	assert.Equal(t, "FCM456", oa.ChannelByID(testdb.AndroidChannel.ID).Config().GetString(models.ChannelConfigFCMID, ""))
}

func TestRelayerSyncOutbox(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	// three queued messages, two of which share their text so can be sent as one broadcast
	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hello", nil, models.MsgStatusQueued, false)
	out2 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-9791-770d-a47d-8f4a6ea3ad13", testdb.AndroidChannel, testdb.Bob, "hello", nil, models.MsgStatusQueued, false)
	out3 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad9-f0bc-7738-8af8-99712a6f8bff", testdb.AndroidChannel, testdb.Cat, "goodbye", nil, models.MsgStatusQueued, false)

	// one that's already been sent, and one on a different channel
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bada-2b39-7cac-9714-827df9ec6b91", testdb.AndroidChannel, testdb.Ann, "already gone", nil, models.MsgStatusSent, false)
	testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bb09-f0e9-7489-a58e-69304a7941a0", testdb.TwilioChannel, testdb.Ann, "not android", nil, models.MsgStatusQueued, false)

	body := `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[],"retry":[]}]}`
	status, resp := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	expected := fmt.Sprintf(`{"cmds":[
		{"cmd":"mt_bcast","msg":"hello","to":[{"phone":"+16055741111","id":%d},{"phone":"+16055742222","id":%d}]},
		{"cmd":"mt_bcast","msg":"goodbye","to":[{"phone":"+16055743333","id":%d}]}
	]}`, out1.ID, out2.ID, out3.ID)
	test.AssertEqualJSON(t, []byte(expected), jsonx.MustMarshal(resp), "unexpected outbox commands")

	// the sync event records how many commands we sent back, not counting acks
	assertdb.Query(t, rt.DB, `SELECT outgoing_command_count FROM channels_syncevent WHERE channel_id = $1`, testdb.AndroidChannel.ID).Returns(2)

	// messages the device tells us it already holds aren't offered again
	body = fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[%d,%d],"retry":[]}]}`, out1.ID, out2.ID)
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	expected = fmt.Sprintf(`{"cmds":[{"cmd":"mt_bcast","msg":"goodbye","to":[{"phone":"+16055743333","id":%d}]}]}`, out3.ID)
	test.AssertEqualJSON(t, []byte(expected), jsonx.MustMarshal(resp), "unexpected outbox commands")

	assertdb.Query(t, rt.DB, `SELECT pending_message_count FROM channels_syncevent ORDER BY id DESC LIMIT 1`).Returns(2)

	// ids an old relayer has mangled into negatives exclude nothing rather than being mapped onto some other
	// message - it'll be re-offered what it already holds, which is the harmless end of that trade
	body = fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[%d,%d],"retry":[%d]}]}`,
		int64(out1.ID)-(1<<32), int64(out2.ID)-(1<<32), int64(out3.ID)-(1<<32))
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)
	assert.Len(t, resp["cmds"], 2)
}

func TestRelayerSyncOnlyItsOwnMessages(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	// a message in the same workspace but on a different channel, which this relayer was never given
	other := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.TwilioChannel, testdb.Ann, "not yours", nil, models.MsgStatusQueued, false)

	body := fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"mt_sent","msg_id":%d,"ts":1746361847000,"p_id":1}]}`, other.ID)
	status, resp := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	// not acked, and left alone - a relayer only gets to decide the status of messages on its own channel
	assert.Equal(t, []any{}, resp["cmds"])
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, other.ID).
		Columns(map[string]any{"status": "Q", "folder": "O"})
}

func TestRelayerSyncClaimAndReset(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	statusCmd := `{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[],"retry":[],"org_id":%d}`

	// a device that thinks it's in a different workspace is told which one it's actually in
	body := fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},`+statusCmd+`]}`, testdb.Org2.ID)
	status, resp := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	expected := fmt.Sprintf(`{"cmds":[{"cmd":"claim","org_id":%d}]}`, testdb.Org1.ID)
	test.AssertEqualJSON(t, []byte(expected), jsonx.MustMarshal(resp), "unexpected claim command")

	// but one that has it right isn't
	body = fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},`+statusCmd+`]}`, testdb.Org1.ID)
	_, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, []any{}, resp["cmds"])

	// a device asking to be reset disconnects the channel
	testdb.InsertIncomingCallTrigger(t, rt, testdb.Org1, testdb.Favorites, nil, nil, testdb.AndroidChannel)

	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"reset","p_id":9}]}`, nil)
	assert.Equal(t, 200, status)
	test.AssertEqualJSON(t, []byte(`{"cmds":[{"cmd":"ack","p_id":9}]}`), jsonx.MustMarshal(resp), "unexpected reset response")

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM channels_channel WHERE id = $1 AND is_active = FALSE`, testdb.AndroidChannel.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM triggers_trigger WHERE channel_id = $1 AND is_active = TRUE`, testdb.AndroidChannel.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM triggers_trigger WHERE channel_id = $1 AND is_archived = TRUE`, testdb.AndroidChannel.ID).Returns(1)

	testsuite.AssertBatchTasks(t, rt, testdb.Org1.ID, map[string]int{"interrupt_channel": 1})

	// and now its relayer is told to stop
	status, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"}]}`, nil)
	assert.Equal(t, 200, status)
	assert.Equal(t, "rel", resp["cmds"].([]any)[0].(map[string]any)["cmd"])

	_ = ctx
}

func TestRelayerSyncOutdatedApp(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	models.FlushLatestAndroidAppVersion()
	t.Cleanup(models.FlushLatestAndroidAppVersion)

	rt.DB.MustExec(`INSERT INTO apks_apk(apk_type, apk_file, version, description, created_on) VALUES('R', 'apks/relayer.apk', '2.0.0', '', NOW())`)

	statusCmd := `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[],"retry":[],"app_version":"%s"}]}`

	// a device running an older app raises an incident
	status, _ := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fmt.Sprintf(statusCmd, "1.9.0"), nil)
	assert.Equal(t, 200, status)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'channel:outdated_app' AND channel_id = $1 AND ended_on IS NULL`, testdb.AndroidChannel.ID).Returns(1)

	// syncing again while still outdated doesn't raise a second one
	relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fmt.Sprintf(statusCmd, "1.9.0"), nil)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'channel:outdated_app' AND channel_id = $1`, testdb.AndroidChannel.ID).Returns(1)

	// and once it's been updated the incident is ended
	relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, fmt.Sprintf(statusCmd, "2.0.0"), nil)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM notifications_incident WHERE incident_type = 'channel:outdated_app' AND channel_id = $1 AND ended_on IS NULL`, testdb.AndroidChannel.ID).Returns(0)

	_ = ctx
}

func TestRelayerSyncDialects(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)

	// the app has sent numbers as numbers, as strings and as floats, and has used long field names - none of which
	// may reject the sync, because a device stuck on one dialect would then never deliver anything again
	body := fmt.Sprintf(`{"cmds":[
		{"cmd":"fcm","fcm_id":"FCM123"},
		{"cmd":"mt_sent","msg_id":"%d","ts":1746361847000.0,"p_id":"7"},
		{"cmd":"status","power_source":"USB","power_status":"FUL","power_level":"55","network_type":"LTE","pending_messages":[],"retry_messages":[]}
	]}`, out1.ID)

	status, resp := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	// the p_id is echoed back exactly as the device sent it, since that's what it matches its pending commands on
	test.AssertEqualJSON(t, []byte(`{"cmds":[{"cmd":"ack","p_id":"7"}]}`), jsonx.MustMarshal(resp), "unexpected ack")

	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out1.ID).
		Columns(map[string]any{"status": "S", "folder": "S"})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE id = $1 AND sent_on = '2025-05-04T12:30:47Z'`, out1.ID).Returns(1)

	// the long field names are read the same as the short ones
	assertdb.Query(t, rt.DB, `SELECT power_source, power_status, power_level, network_type FROM channels_syncevent WHERE channel_id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"power_source": "USB", "power_status": "FUL", "power_level": 55, "network_type": "LTE"})

	// and a value we can't make any sense of doesn't take the rest of the sync down with it
	body = `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":"eighty","net":"WIFI","pending":[],"retry":[]}]}`
	status, _ = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)

	assertdb.Query(t, rt.DB, `SELECT power_level FROM channels_syncevent ORDER BY id DESC LIMIT 1`).Returns(0)

	// a device reporting itself in workspace 0 is still told where it actually is
	body = `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[],"retry":[],"org_id":0}]}`
	_, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	test.AssertEqualJSON(t, []byte(fmt.Sprintf(`{"cmds":[{"cmd":"claim","org_id":%d}]}`, testdb.Org1.ID)), jsonx.MustMarshal(resp), "unexpected claim command")

	// an empty short-form list means empty, rather than falling back to the long-form one
	body = fmt.Sprintf(`{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"},{"cmd":"status","p_src":"BAT","p_sts":"CHA","p_lvl":80,"net":"WIFI","pending":[],"retry":[],"pending_messages":[%d]}]}`, out1.ID)
	_, resp = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assertdb.Query(t, rt.DB, `SELECT pending_message_count FROM channels_syncevent ORDER BY id DESC LIMIT 1`).Returns(0)
}

func TestRelayerSyncPreservesStatusesOnFailure(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	out1 := testdb.InsertOutgoingMsg(t, rt, testdb.Org1, "0199bad8-f98d-75a3-b641-2718a25ac3f5", testdb.AndroidChannel, testdb.Ann, "hi", nil, models.MsgStatusQueued, false)

	// the channel is disabled, so it isn't in assets and anything needing a contact will fail
	rt.DB.MustExec(`UPDATE channels_channel SET is_enabled = FALSE WHERE id = $1`, testdb.AndroidChannel.ID)
	models.FlushCache()

	body := fmt.Sprintf(`{"cmds":[
		{"cmd":"fcm","fcm_id":"FCM123"},
		{"cmd":"mt_sent","msg_id":%d,"ts":1746361847000,"p_id":1},
		{"cmd":"mo_sms","phone":"+593979000111","msg":"incoming!","ts":1746361848000,"p_id":2}
	]}`, out1.ID)

	status, _ := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 500, status)

	// the status change we'd already accepted was still written, so the device isn't asked to send it again
	assertdb.Query(t, rt.DB, `SELECT status, folder FROM msgs_msg WHERE id = $1`, out1.ID).
		Columns(map[string]any{"status": "S", "folder": "S"})
}

func TestRelayerSyncHostilePayload(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	startServer(t, rt)
	claimAndroidChannel(t, rt)

	// every one of these fields goes into a column that would reject it, and a rejected insert would fail this
	// device's syncs permanently rather than just losing the odd value
	body := fmt.Sprintf(`{"cmds":[
		{"cmd":"fcm","fcm_id":"FCM123","uuid":"not-a-uuid"},
		{"cmd":"status","p_src":"%s","p_sts":"%s","p_lvl":99999999999,"net":"%s","dev":"%s","os":"%s\u0000","pending":[],"retry":[]}
	]}`, strings.Repeat("S", 200), strings.Repeat("T", 200), strings.Repeat("N", 300), strings.Repeat("D", 500), strings.Repeat("O", 500))

	status, resp := relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, body, nil)
	assert.Equal(t, 200, status)
	assert.Equal(t, []any{}, resp["cmds"])

	// the values were clamped to what the columns take rather than blowing up the sync
	assertdb.Query(t, rt.DB, `SELECT length(power_source) AS src, length(power_status) AS sts, power_level, length(network_type) AS net FROM channels_syncevent WHERE channel_id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"src": 64, "sts": 64, "power_level": 100, "net": 128})

	// the unusable uuid was ignored rather than written, and the fcm id it came with still landed
	assertdb.Query(t, rt.DB, `SELECT uuid::text FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns(string(testdb.AndroidChannel.UUID))
	assertdb.Query(t, rt.DB, `SELECT config->>'FCM_ID' FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).Returns("FCM123")

	// the null byte in the os string didn't reach the column either
	assertdb.Query(t, rt.DB, `SELECT length(device) AS dev, length(os) AS os FROM channels_channel WHERE id = $1`, testdb.AndroidChannel.ID).
		Columns(map[string]any{"dev": 255, "os": 255})

	// and the device is still able to sync afterwards
	status, _ = relayerSync(t, rt, testdb.AndroidChannel.ID, relayerSecret, `{"cmds":[{"cmd":"fcm","fcm_id":"FCM123"}]}`, nil)
	assert.Equal(t, 200, status)
}
