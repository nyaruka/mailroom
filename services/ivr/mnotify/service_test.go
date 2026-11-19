package mnotify_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/core/hints"
	"github.com/nyaruka/mailroom/v26/core/ivr"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/services/ivr/mnotify"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionsForSprint(t *testing.T) {
	cfg := runtime.NewDefaultConfig()
	cfg.AttachmentDomain = "mailroom.io"

	urn := urns.URN("tel:+233541234567")
	expiresOn := time.Now().Add(time.Hour)
	channelRef := assets.NewChannelReference(assets.ChannelUUID(uuids.NewV4()), "mNotify Channel")

	tcs := []struct {
		label    string
		events   []events.Event
		expected string
	}{
		{
			label: "msg without audio is skipped",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "Hi there", "", "")),
			},
			expected: `[{"hangup":{}}]`,
		},
		{
			label: "msg with audio",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "Hi there", "/recordings/foo.wav", "eng-US")),
			},
			expected: `[{"playback":{"url":"https://mailroom.io/recordings/foo.wav"}},{"hangup":{}}]`,
		},
		{
			label: "2 msgs with audio then wait for a digit",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "hello world", "/recordings/hello.wav", "")),
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "enter a number", "/recordings/enter.wav", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewFixedDigits(1)),
			},
			expected: `[{"playback":{"url":"https://mailroom.io/recordings/hello.wav"}},{"get_digits":{"url":"https://mailroom.io/recordings/enter.wav","num_digits":1,"timeout":30000}}]`,
		},
		{
			label: "wait for terminated digits",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "enter a number, then press #", "/recordings/enter.wav", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewTerminatedDigits("#")),
			},
			expected: `[{"get_digits":{"url":"https://mailroom.io/recordings/enter.wav","num_digits":32,"timeout":30000,"finish_on_key":"#"}}]`,
		},
		{
			label: "wait for digits without a count or a key to end on",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "enter a number", "/recordings/enter.wav", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewTerminatedDigits("")),
			},
			expected: `[{"get_digits":{"url":"https://mailroom.io/recordings/enter.wav","num_digits":32,"timeout":30000,"finish_on_key":"#"}}]`,
		},
		{
			label: "wait for digits with nothing to play",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "enter a number", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewFixedDigits(4)),
			},
			expected: `[{"get_digits":{"num_digits":4,"timeout":30000,"skip_audio":true}}]`,
		},
		{
			label: "wait for recording",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "say something", "/recordings/say.wav", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewAudio()),
			},
			expected: `[{"record":{"url":"https://mailroom.io/recordings/say.wav","max_duration":600,"play_beep":true}}]`,
		},
		{
			label: "dial wait isn't supported and ends call",
			events: []events.Event{
				events.NewIVRCreated(core.NewIVRMsgOut(urn, channelRef, "please hold", "/recordings/hold.wav", "")),
				events.NewDialWait(urns.URN(`tel:+12345550100`), 60, 7200, expiresOn),
			},
			expected: `[{"playback":{"url":"https://mailroom.io/recordings/hold.wav"}},{"hangup":{}}]`,
		},
	}

	for _, tc := range tcs {
		actions, err := mnotify.ActionsForSprint(cfg, tc.events)
		assert.NoError(t, err, "%s: unexpected error", tc.label)
		assert.JSONEq(t, tc.expected, string(jsonx.MustMarshal(actions)), "%s: unexpected actions", tc.label)
	}
}

func TestService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	ch := testdb.InsertChannel(t, rt, testdb.Org1, "MN", "mNotify", "+233501234567", []string{"tel"}, "CA", map[string]any{"api_key": "key123", "secret": "sesame"})
	rt.DB.MustExec(`UPDATE channels_channel SET country = 'GH' WHERE id = $1`, ch.ID)
	testCall := testdb.InsertCall(t, rt, testdb.Org1, ch, testdb.Ann)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	svc, err := ivr.GetService(http.DefaultClient, oa.ChannelByUUID(ch.UUID))
	require.NoError(t, err)

	router := svc.(ivr.CallbackRouter)

	request := func(query string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "https://mailroom.io/mr/ivr/c/"+string(ch.UUID)+"/callback?"+query, nil)
	}

	// requests are authenticated by token
	assert.NoError(t, svc.ValidateRequestSignature(request("token=sesame")))
	assert.EqualError(t, svc.ValidateRequestSignature(request("token=sesam")), "invalid token")
	assert.EqualError(t, svc.ValidateRequestSignature(request("")), "invalid token")

	// what a request is depends on its params
	assert.Equal(t, ivr.ActionIncoming, router.CallbackAction(request("IsStarting=true&context=incoming")))
	assert.Equal(t, ivr.ActionIncoming, router.CallbackAction(request("IsStarting=true&context=from-trunk")))
	assert.Equal(t, ivr.ActionIncoming, router.CallbackAction(request("IsStarting=true")))
	assert.Equal(t, ivr.ActionStart, router.CallbackAction(request("IsStarting=true&context=outgoing")))
	assert.Equal(t, ivr.ActionStart, router.CallbackAction(request("IsStarting=true&uniqueId=0199bad8-f98d-75a3-b641-2718a25ac3f5")))
	assert.Equal(t, ivr.ActionResume, router.CallbackAction(request("IsStarting=false&context=outgoing&action=get_digits&DTMF=1")))
	assert.Equal(t, ivr.ActionStatus, router.CallbackAction(request("disposition=BUSY")))
	assert.Equal(t, ivr.ActionStatus, router.CallbackAction(request("action=hangup")))
	assert.Equal(t, ivr.ActionResume, router.CallbackAction(request("action=playback")))
	assert.Equal(t, ivr.ActionResume, router.CallbackAction(request("action=record&recordingUrl=http%3A%2F%2Fx.com%2Fa.wav")))
	assert.Equal(t, ivr.ActionNone, router.CallbackAction(request("action=dial")))
	assert.Equal(t, ivr.ActionNone, router.CallbackAction(request("SequenceID=123")))

	assert.Equal(t, core.CallUUID("0199bad8-f98d-75a3-b641-2718a25ac3f5"), router.CallUUIDForRequest(request("uniqueId=0199bad8-f98d-75a3-b641-2718a25ac3f5")))
	assert.Equal(t, core.CallUUID(""), router.CallUUIDForRequest(request("SequenceID=123")))

	// the contact is whichever number isn't ours
	urn, err := svc.URNForRequest(request("CallerId=233541234567&Extension=233501234567"))
	assert.NoError(t, err)
	assert.Equal(t, urns.URN("tel:+233541234567"), urn)

	urn, err = svc.URNForRequest(request("CallerId=0501234567&Extension=233541234567"))
	assert.NoError(t, err)
	assert.Equal(t, urns.URN("tel:+233541234567"), urn)

	urn, err = svc.URNForRequest(request("CallerId=0541234567&Extension=0501234567"))
	assert.NoError(t, err)
	assert.Equal(t, urns.URN("tel:+233541234567"), urn)

	_, err = svc.URNForRequest(request("SequenceID=123"))
	assert.EqualError(t, err, "no caller number found in request")

	callID, err := svc.CallIDForRequest(request("SequenceID=123"))
	assert.NoError(t, err)
	assert.Equal(t, "123", callID)

	// statuses come from dispositions
	statusTcs := []struct {
		query    string
		status   models.CallStatus
		error    models.CallError
		duration int
	}{
		{"action=get_digits&DTMF=1", models.CallStatusInProgress, "", 0},
		{"disposition=ANSWERED&duration=95", models.CallStatusCompleted, "", 95},
		{"disposition=NO+ANSWER", models.CallStatusErrored, models.CallErrorNoAnswer, 0},
		{"disposition=BUSY", models.CallStatusErrored, models.CallErrorBusy, 0},
		{"disposition=FAILED", models.CallStatusErrored, models.CallErrorProvider, 0},
		{"disposition=CONGESTION", models.CallStatusFailed, models.CallErrorProvider, 0},
		{"action=hangup", models.CallStatusCompleted, "", 0},
	}
	for _, tc := range statusTcs {
		status, errReason, duration := svc.StatusForRequest(request(tc.query))
		assert.Equal(t, tc.status, status, "status mismatch for %s", tc.query)
		assert.Equal(t, tc.error, errReason, "error mismatch for %s", tc.query)
		assert.Equal(t, tc.duration, duration, "duration mismatch for %s", tc.query)
	}

	// resumes come from the action that ended
	resume, err := svc.ResumeForRequest(request("action=get_digits&DTMF=0123"))
	assert.NoError(t, err)
	assert.Equal(t, ivr.InputResume{Input: "0123"}, resume)

	resume, err = svc.ResumeForRequest(request("action=get_digits"))
	assert.NoError(t, err)
	assert.Equal(t, ivr.InputResume{}, resume)

	resume, err = svc.ResumeForRequest(request("action=record&recordingUrl=https%3A%2F%2Fexample.com%2Frecording.wav"))
	assert.NoError(t, err)
	assert.Equal(t, ivr.InputResume{Attachment: "audio:https://example.com/recording.wav"}, resume)

	_, err = svc.ResumeForRequest(request("action=dial"))
	assert.EqualError(t, err, "unable to resume for action: dial")

	// queued actions are returned one at a time as playbacks end
	call, err := models.GetCallByUUID(ctx, rt.DB, testdb.Org1.ID, testCall.UUID)
	require.NoError(t, err)

	vc := rt.VK.Get()
	defer vc.Close()

	key := "mnotify:actions:" + string(testCall.UUID)
	_, err = vc.Do("RPUSH", key, `{"actions":{"playback":{"url":"https://mailroom.io/2.wav"}}}`, `{"actions":{"hangup":{}}}`)
	require.NoError(t, err)

	body, err := svc.PreprocessResume(ctx, rt, call, request("action=get_digits&DTMF=1"))
	assert.NoError(t, err)
	assert.Nil(t, body)

	body, err = svc.PreprocessResume(ctx, rt, call, request("action=playback"))
	assert.NoError(t, err)
	assert.JSONEq(t, `{"actions":{"playback":{"url":"https://mailroom.io/2.wav"}}}`, string(body))

	body, err = svc.PreprocessResume(ctx, rt, call, request("action=playback"))
	assert.NoError(t, err)
	assert.JSONEq(t, `{"actions":{"hangup":{}}}`, string(body))

	body, err = svc.PreprocessResume(ctx, rt, call, request("action=playback"))
	assert.NoError(t, err)
	assert.JSONEq(t, `{"actions":{"hangup":{}},"message":"no more actions for call"}`, string(body))

	// responses that don't come from a session all end the call
	rec := httptest.NewRecorder()
	assert.NoError(t, svc.WriteErrorResponse(rec, assert.AnError))
	assert.JSONEq(t, `{"actions":{"hangup":{}},"message":"assert.AnError general error for testing"}`, rec.Body.String())

	assert.Equal(t, []string{"key123", "sesame"}, svc.RedactValues(oa.ChannelByUUID(ch.UUID)))
}
