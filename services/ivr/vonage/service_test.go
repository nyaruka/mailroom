package vonage

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/routers/waits/hints"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseForSprint(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	mockVonage := httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://api.nexmo.com/v1/calls": {
			httpx.NewMockResponse(201, nil, []byte(`{"uuid": "63f61863-4a51-4f6b-86e1-46edebcf9356", "status": "started", "direction": "outbound"}`)),
		},
	})

	defer httpx.SetRequestor(httpx.DefaultRequestor)
	httpx.SetRequestor(mockVonage)

	urn := urns.URN("tel:+12067799294")
	expiresOn := time.Now().Add(time.Hour)
	channelRef := assets.NewChannelReference(testdb.VonageChannel.UUID, "Vonage Channel")

	resumeURL := "http://temba.io/resume?session=1"

	// deactivate our twilio channel
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.TwilioChannel.ID)

	// update callback domain and roles for channel
	rt.DB.MustExec(`UPDATE channels_channel SET config = config || '{"callback_domain": "localhost:8091"}'::jsonb, role='SRCA' WHERE id = $1`, testdb.VonageChannel.ID)

	// set our UUID generator
	uuids.SetGenerator(uuids.NewSeededGenerator(0, time.Now))

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	channel := oa.ChannelByUUID(testdb.VonageChannel.UUID)
	assert.NotNil(t, channel)

	p, err := NewServiceFromChannel(http.DefaultClient, channel)
	require.NoError(t, err)

	provider := p.(*service)

	bob, _, bobURNs := testdb.Bob.Load(t, rt, oa)

	trigger := triggers.NewBuilder(testdb.Favorites.Reference()).Manual().Build()
	call := models.NewOutgoingCall(testdb.Org1.ID, oa.ChannelByUUID(testdb.VonageChannel.UUID), bob, bobURNs[0].ID, trigger)
	err = models.InsertCalls(ctx, rt.DB, []*models.Call{call})
	assert.NoError(t, err)

	indentMarshal = false

	tcs := []struct {
		events   []flows.Event
		expected string
	}{
		{ // 0
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "hello world", "", "")),
			},
			`[{"action":"talk","text":"hello world"}]`,
		},
		{ // 1
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "hello world", "/recordings/foo.wav", "")),
			},
			`[{"action":"stream","streamUrl":["/recordings/foo.wav"]}]`,
		},
		{ // 2
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "hello world", "https://temba.io/recordings/foo.wav", "")),
			},
			`[{"action":"stream","streamUrl":["https://temba.io/recordings/foo.wav"]}]`,
		},
		{ // 3
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "hello world", "", "")),
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "goodbye", "", "")),
			},
			`[{"action":"talk","text":"hello world"},{"action":"talk","text":"goodbye"}]`,
		},
		{ // 4
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "enter a number", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewFixedDigits(1)),
			},
			`[{"action":"talk","text":"enter a number","bargeIn":true},{"action":"input","maxDigits":1,"submitOnHash":true,"timeOut":30,"eventUrl":["http://temba.io/resume?session=1\u0026wait_type=gather\u0026sig=4Yil1wUntXd%2F7AQx%2Bt0rkwihx%2Fg%3D"],"eventMethod":"POST"}]`,
		},
		{ // 5
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "enter a number, then press #", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewTerminatedDigits("#")),
			},
			`[{"action":"talk","text":"enter a number, then press #","bargeIn":true},{"action":"input","maxDigits":20,"submitOnHash":true,"timeOut":30,"eventUrl":["http://temba.io/resume?session=1\u0026wait_type=gather\u0026sig=4Yil1wUntXd%2F7AQx%2Bt0rkwihx%2Fg%3D"],"eventMethod":"POST"}]`,
		},
		{ // 6
			[]flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "say something", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewAudio()),
			},
			`[{"action":"talk","text":"say something"},{"action":"record","endOnKey":"#","timeOut":600,"endOnSilence":5,"eventUrl":["http://temba.io/resume?session=1\u0026wait_type=recording_url\u0026recording_uuid=3801aaca-eedf-4d5b-9066-64e8c0e4a771\u0026sig=S9SN7OELddL6zxiZTPZsCNfKFMw%3D"],"eventMethod":"POST"},{"action":"input","submitOnHash":true,"timeOut":1,"eventUrl":["http://temba.io/resume?session=1\u0026wait_type=record\u0026recording_uuid=3801aaca-eedf-4d5b-9066-64e8c0e4a771\u0026sig=YLz9dq5KiI3sVTC9O2vrgCSdAqc%3D"],"eventMethod":"POST"}]`,
		},
		{ // 7
			[]flows.Event{
				events.NewDialWait(urns.URN(`tel:+1234567890`), 60, 7200, expiresOn),
			},
			`[{"action":"conversation","name":"ece0b8b7-c196-4d91-8125-1b7c9c9ca520"}]`,
		},
	}

	for i, tc := range tcs {
		response, err := provider.responseForSprint(ctx, rt.VK, channel, call, resumeURL, tc.events)
		assert.NoError(t, err, "%d: unexpected error")
		assert.Equal(t, tc.expected, response, "%d: unexpected response", i)
	}

	// the dial action will have made a call to the calls endpoint
	assert.Equal(t, 1, len(mockVonage.Requests()))
	body, _ := io.ReadAll(mockVonage.Requests()[0].Body)
	var decodedBody map[string]any
	jsonx.MustUnmarshal(body, &decodedBody)
	assert.Equal(t, float64(60), decodedBody["ringing_timer"])
	assert.Equal(t, float64(7200), decodedBody["length_timer"])
}

func TestRedactValues(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)
	ch := oa.ChannelByUUID(testdb.VonageChannel.UUID)
	svc, _ := ivr.GetService(ch)

	assert.NotNil(t, svc)
	assert.Equal(t, []string{"-----BEGIN PRIVATE KEY-----\nMIICdgIBADANBgkqhkiG9w0BAQEFAASCAmAwggJcAgEAAoGBAKNwapOQ6rQJHetP\nHRlJBIh1OsOsUBiXb3rXXE3xpWAxAha0MH+UPRblOko+5T2JqIb+xKf9Vi3oTM3t\nKvffaOPtzKXZauscjq6NGzA3LgeiMy6q19pvkUUOlGYK6+Xfl+B7Xw6+hBMkQuGE\nnUS8nkpR5mK4ne7djIyfHFfMu4ptAgMBAAECgYA+s0PPtMq1osG9oi4xoxeAGikf\nJB3eMUptP+2DYW7mRibc+ueYKhB9lhcUoKhlQUhL8bUUFVZYakP8xD21thmQqnC4\nf63asad0ycteJMLb3r+z26LHuCyOdPg1pyLk3oQ32lVQHBCYathRMcVznxOG16VK\nI8BFfstJTaJu0lK/wQJBANYFGusBiZsJQ3utrQMVPpKmloO2++4q1v6ZR4puDQHx\nTjLjAIgrkYfwTJBLBRZxec0E7TmuVQ9uJ+wMu/+7zaUCQQDDf2xMnQqYknJoKGq+\noAnyC66UqWC5xAnQS32mlnJ632JXA0pf9pb1SXAYExB1p9Dfqd3VAwQDwBsDDgP6\nHD8pAkEA0lscNQZC2TaGtKZk2hXkdcH1SKru/g3vWTkRHxfCAznJUaza1fx0wzdG\nGcES1Bdez0tbW4llI5By/skZc2eE3QJAFl6fOskBbGHde3Oce0F+wdZ6XIJhEgCP\niukIcKZoZQzoiMJUoVRrA5gqnmaYDI5uRRl/y57zt6YksR3KcLUIuQJAd242M/WF\n6YAZat3q/wEeETeQq1wrooew+8lHl05/Nt0cCpV48RGEhJ83pzBm3mnwHf8lTBJH\nx6XroMXsmbnsEw==\n-----END PRIVATE KEY-----"}, svc.RedactValues(ch))
}
