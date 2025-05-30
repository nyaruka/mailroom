package bandwidth_test

import (
	"encoding/xml"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/routers/waits/hints"
	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/services/ivr/bandwidth"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestResponseForSprint(t *testing.T) {
	_, rt := testsuite.Runtime()

	urn := urns.URN("tel:+12067799294")
	expiresOn := time.Now().Add(time.Hour)
	channelRef := assets.NewChannelReference(assets.ChannelUUID(uuids.NewV4()), "Bandwidth Channel")
	env := envs.NewBuilder().WithAllowedLanguages("eng", "spa").WithDefaultCountry("US").Build()

	resumeURL := "http://temba.io/resume?session=1"

	// set our attachment domain for testing
	rt.Config.AttachmentDomain = "mailroom.io"
	defer func() { rt.Config.AttachmentDomain = "" }()

	tcs := []struct {
		events   []flows.Event
		expected string
	}{
		{
			// ivr msg, no text language specified
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Hi there", "", "")),
			},
			expected: `<Response><SpeakSentence locale="en_US">Hi there</SpeakSentence><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg, supported text language specified
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Hi there", "", "eng-GB")),
			},
			expected: `<Response><SpeakSentence locale="en_GB">Hi there</SpeakSentence><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg, unsupported text language specified
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Amakuru", "", "kin")),
			},
			expected: `<Response><SpeakSentence locale="en_US">Amakuru</SpeakSentence><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg with audio attachment, text language ignored
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Hi there", "/recordings/foo.wav", "eng-US")),
			},
			expected: `<Response><PlayAudio>https://mailroom.io/recordings/foo.wav</PlayAudio><Hangup></Hangup></Response>`,
		},
		{
			// 2 ivr msgs
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "hello world", "", "")),
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "goodbye", "", "")),
			},
			expected: `<Response><SpeakSentence locale="en_US">hello world</SpeakSentence><SpeakSentence locale="en_US">goodbye</SpeakSentence><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg followed by wait for digits
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "enter a number", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewFixedDigitsHint(1)),
			},
			expected: `<Response><Gather maxDigits="1" interDigitTimeout="30" gatherUrl="http://temba.io/resume?session=1&amp;wait_type=gather"><SpeakSentence locale="en_US">enter a number</SpeakSentence></Gather><Redirect redirectUrl="http://temba.io/resume?session=1&amp;wait_type=gather&amp;timeout=true"></Redirect></Response>`,
		},
		{
			// ivr msg followed by wait for terminated digits
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "enter a number, then press #", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewTerminatedDigitsHint("#")),
			},
			expected: `<Response><Gather terminatingDigits="#" interDigitTimeout="30" gatherUrl="http://temba.io/resume?session=1&amp;wait_type=gather"><SpeakSentence locale="en_US">enter a number, then press #</SpeakSentence></Gather><Redirect redirectUrl="http://temba.io/resume?session=1&amp;wait_type=gather&amp;timeout=true"></Redirect></Response>`,
		},
		{
			// ivr msg followed by wait for recording
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "say something", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewAudioHint()),
			},
			expected: `<Response><SpeakSentence locale="en_US">say something</SpeakSentence><Record recordCompleteUrl="http://temba.io/resume?session=1&amp;wait_type=record" maxDuration="600"></Record><Redirect redirectUrl="http://temba.io/resume?session=1&amp;wait_type=record&amp;empty=true"></Redirect></Response>`,
		},
		{
			// dial wait
			events: []flows.Event{
				events.NewDialWait(urns.URN(`tel:+1234567890`), 60, 7200, expiresOn),
			},
			expected: `<Response><Transfer transferCompleteUrl="http://temba.io/resume?session=1&amp;wait_type=dial" callTimeout="60" timeLimit="7200"><PhoneNumber>+1234567890</PhoneNumber></Transfer></Response>`,
		},
	}

	for i, tc := range tcs {
		response, err := bandwidth.ResponseForSprint(rt, env, urn, resumeURL, tc.events, false)
		assert.NoError(t, err, "%d: unexpected error")
		assert.Equal(t, xml.Header+tc.expected, response, "%d: unexpected response", i)
	}
}

func TestRedactValues(t *testing.T) {
	_, rt := testsuite.Runtime()

	bwChannel := testdb.InsertChannel(rt, testdb.Org1, "BW", "Bandwidth", "123", []string{"tel"}, "CASR",
		map[string]any{"username": "user", "password": "pass", "voice_application_id": "app-id", "account_id": "acc-id"})

	oa := testdb.Org1.Load(rt)
	ch := oa.ChannelByUUID(bwChannel.UUID)
	svc, _ := ivr.GetService(ch)

	assert.Equal(t, []string{"dXNlcjpwYXNz", "pass"}, svc.RedactValues(ch))
}
