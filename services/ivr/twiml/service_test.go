package twiml_test

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
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
	"github.com/nyaruka/mailroom/services/ivr/twiml"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestResponseForSprint(t *testing.T) {
	_, rt := testsuite.Runtime()

	urn := urns.URN("tel:+12067799294")
	expiresOn := time.Now().Add(time.Hour)
	channelRef := assets.NewChannelReference(assets.ChannelUUID(uuids.NewV4()), "Twilio Channel")
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
			expected: `<Response><Say language="en-US">Hi there</Say><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg, supported text language specified
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Hi there", "", "eng-GB")),
			},
			expected: `<Response><Say language="en-GB">Hi there</Say><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg, unsupported text language specified
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Amakuru", "", "kin")),
			},
			expected: `<Response><Say language="en-US">Amakuru</Say><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg with audio attachment, text language ignored
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "Hi there", "/recordings/foo.wav", "eng-US")),
			},
			expected: `<Response><Play>https://mailroom.io/recordings/foo.wav</Play><Hangup></Hangup></Response>`,
		},
		{
			// 2 ivr msgs
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "hello world", "", "")),
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "goodbye", "", "")),
			},
			expected: `<Response><Say language="en-US">hello world</Say><Say language="en-US">goodbye</Say><Hangup></Hangup></Response>`,
		},
		{
			// ivr msg followed by wait for digits
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "enter a number", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewFixedDigitsHint(1)),
			},
			expected: `<Response><Gather numDigits="1" timeout="30" action="http://temba.io/resume?session=1&amp;wait_type=gather"><Say language="en-US">enter a number</Say></Gather><Redirect>http://temba.io/resume?session=1&amp;wait_type=gather&amp;timeout=true</Redirect></Response>`,
		},
		{
			// ivr msg followed by wait for terminated digits
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "enter a number, then press #", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewTerminatedDigitsHint("#")),
			},
			expected: `<Response><Gather finishOnKey="#" timeout="30" action="http://temba.io/resume?session=1&amp;wait_type=gather"><Say language="en-US">enter a number, then press #</Say></Gather><Redirect>http://temba.io/resume?session=1&amp;wait_type=gather&amp;timeout=true</Redirect></Response>`,
		},
		{
			// ivr msg followed by wait for recording
			events: []flows.Event{
				events.NewIVRCreated(flows.NewIVRMsgOut(urn, channelRef, "say something", "", "")),
				events.NewMsgWait(nil, expiresOn, hints.NewAudioHint()),
			},
			expected: `<Response><Say language="en-US">say something</Say><Record action="http://temba.io/resume?session=1&amp;wait_type=record" maxLength="600"></Record><Redirect>http://temba.io/resume?session=1&amp;wait_type=record&amp;empty=true</Redirect></Response>`,
		},
		{
			// dial wait
			events: []flows.Event{
				events.NewDialWait(urns.URN(`tel:+1234567890`), 60, 7200, expiresOn),
			},
			expected: `<Response><Dial action="http://temba.io/resume?session=1&amp;wait_type=dial" timeout="60" timeLimit="7200">+1234567890</Dial></Response>`,
		},
	}

	for i, tc := range tcs {
		response, err := twiml.ResponseForSprint(rt, env, urn, resumeURL, tc.events, false)
		assert.NoError(t, err, "%d: unexpected error")
		assert.Equal(t, xml.Header+tc.expected, response, "%d: unexpected response", i)
	}
}

func TestURNForRequest(t *testing.T) {
	s := twiml.NewService(http.DefaultClient, "12345", "sesame")

	makeRequest := func(body string) *http.Request {
		r, _ := http.NewRequest("POST", "http://textit.com/12345/incoming", strings.NewReader(body))
		r.Header.Add("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Add("Content-Length", strconv.Itoa(len(body)))
		return r
	}

	urn, err := s.URNForRequest(makeRequest(`CallSid=12345&AccountSid=23456&Caller=%2B12064871234&To=%2B12029795079&Called=%2B12029795079&CallStatus=queued&ApiVersion=2010-04-01&Direction=inbound`))
	assert.NoError(t, err)
	assert.Equal(t, urns.URN(`tel:+12064871234`), urn)

	// SignalWire uses From instead of Caller
	urn, err = s.URNForRequest(makeRequest(`CallSid=12345&AccountSid=23456&From=%2B12064871234&To=%2B12029795079&Called=%2B12029795079&CallStatus=queued&ApiVersion=2010-04-01&Direction=inbound`))
	assert.NoError(t, err)
	assert.Equal(t, urns.URN(`tel:+12064871234`), urn)

	_, err = s.URNForRequest(makeRequest(`CallSid=12345&AccountSid=23456&To=%2B12029795079&Called=%2B12029795079&CallStatus=queued&ApiVersion=2010-04-01&Direction=inbound`))
	assert.EqualError(t, err, "no Caller or From parameter found in request")
}

func TestRedactValues(t *testing.T) {
	_, rt := testsuite.Runtime()

	oa := testdata.Org1.Load(rt)
	ch := oa.ChannelByUUID(testdata.TwilioChannel.UUID)
	svc, _ := ivr.GetService(ch)

	assert.Equal(t, []string{"U0lEMTIzNDU2Nzg5OnNlc2FtZQ==", "sesame"}, svc.RedactValues(ch))
}
