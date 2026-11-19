package simulation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/stretchr/testify/assert"
)

const (
	startBody = `
	{
		"org_id": 1,
		"contact": {
			"uuid": "ba96bf7f-bc2a-4873-a7c7-254d1927c4e3",
			"id": 1234567,
			"name": "Ben Haggerty",
			"status": "active",
			"language": "eng",
			"timezone": "America/Guayaquil",
			"urns": [
				"tel:+12065550100"
			],
			"fields": {},
			"created_on": "2000-01-01T00:00:00.000000000-00:00"
		},
		"trigger": {
			"flow": {
				"name": "Favorites",
				"uuid": "9de3663f-c5c5-4c92-9f45-ecbc09abcc85"
			},
			"triggered_on": "2000-01-01T00:00:00.000000000-00:00",
			"type": "manual"
		}
	}`

	resumeBody = `
	{
		"org_id": 1,
		"contact": {
			"uuid": "ba96bf7f-bc2a-4873-a7c7-254d1927c4e3",
			"id": 1234567,
			"name": "Ben Haggerty",
			"status": "active",
			"language": "eng",
			"timezone": "America/Guayaquil",
			"urns": [
				"tel:+12065550100"
			],
			"fields": {},
			"created_on": "2000-01-01T00:00:00.000000000-00:00"
		},
		"resume": {
			"event": {
				"uuid": "019838b3-c982-7adc-a11b-c39e57005264",
				"type": "msg_received",
				"created_on": "2000-01-01T00:00:00.000000000-00:00",
				"msg": {
					"channel": {
						"name": "Twitter",
						"uuid": "0f661e8b-ea9d-4bd3-9953-d368340acf91"
					},
					"text": "$$MESSAGE$$",
					"urn": "tel:+12065550100"
				}
			},
            "resumed_on": "2000-01-01T00:00:00.000000000-00:00",
            "type": "msg"
		},
		"assets": {
			"channels": [
				{
					"uuid": "440099cf-200c-4d45-a8e7-4a564f4a0e8b",
					"name": "Test Channel",
					"address": "+12065550101",
					"schemes": ["tel"],
					"roles": ["send", "receive", "call"],
					"country": "US"
				}
			]
		},
		"session": $$SESSION$$
	}`

	customStartBody = `
	{
		"org_id": 1,
		"contact": {
			"uuid": "ba96bf7f-bc2a-4873-a7c7-254d1927c4e3",
			"id": 1234567,
			"name": "Ben Haggerty",
			"status": "active",
			"language": "eng",
			"timezone": "America/Guayaquil",
			"urns": [
				"tel:+12065550100"
			],
			"fields": {},
			"created_on": "2000-01-01T00:00:00.000000000-00:00"
		},
		"trigger": {
			"event": {
				"uuid": "019838b2-fcda-76a1-a692-3dcba5ec4eaa",
				"type": "msg_received",
				"created_on": "2000-01-01T00:00:00.000000000-00:00",
				"msg": {
					"uuid": "2d611e17-fb22-457f-b802-b8f7ec5cda5b",
					"channel": {"uuid": "440099cf-200c-4d45-a8e7-4a564f4a0e8b", "name": "Test Channel"},
					"urn": "tel:+12065550100",
					"text": "hi there"
				}
			},
			"flow": {
				"name": "Favorites",
				"uuid": "9de3663f-c5c5-4c92-9f45-ecbc09abcc85"
			},
			"triggered_on": "2000-01-01T00:00:00.000000000-00:00",
			"type": "msg"
		},
		"assets": {
			"channels": [
				{
					"uuid": "440099cf-200c-4d45-a8e7-4a564f4a0e8b",
					"name": "Test Channel",
					"address": "+12065550101",
					"schemes": ["tel"],
					"roles": ["send", "receive", "call"],
					"country": "US"
				}
			]
		}
	}`
)

func TestServer(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wg := &sync.WaitGroup{}

	server := web.NewServer(ctx, rt, wg)
	server.Start()

	// give our server time to start
	time.Sleep(time.Second)

	defer server.Stop()

	var session json.RawMessage

	// add a trigger for our campaign flow with 'trigger'
	testdb.InsertKeywordTrigger(t, rt, testdb.Org1, testdb.BackgroundFlow, []string{"trigger"}, models.MatchOnly, nil, nil, nil)

	// and a trigger which will trigger an IVR flow
	testdb.InsertKeywordTrigger(t, rt, testdb.Org1, testdb.IVRFlow, []string{"ivr"}, models.MatchOnly, nil, nil, nil)

	// also add a catch all
	testdb.InsertCatchallTrigger(t, rt, testdb.Org1, testdb.BackgroundFlow, nil, nil, nil)

	tcs := []struct {
		URL              string
		Method           string
		Body             string
		Message          string
		ExpectedStatus   int
		ExpectedResponse string
	}{
		{"/mi/sim/start", "GET", "", "", 405, "illegal"},
		{"/mi/sim/start", "POST", startBody, "", 200, "What is your favorite color?"},
		{"/mi/sim/resume", "POST", resumeBody, "I like blue!", 200, "Good choice, I like Blue too! What is your favorite beer?"},

		// start flow again but resume with a message that matches the campaign flow trigger
		{"/mi/sim/start", "POST", startBody, "", 200, "What is your favorite color?"},
		{"/mi/sim/resume", "POST", resumeBody, "trigger", 200, "Nothing to see here"},
		{"/mi/sim/resume", "POST", resumeBody, "I like blue!", 200, "Nothing to see here"},

		// start favorties again but this time resume with a message that matches the IVR flow trigger
		{"/mi/sim/start", "POST", startBody, "", 200, "What is your favorite color?"},
		{"/mi/sim/resume", "POST", resumeBody, "ivr", 200, "Hello there. Please enter one or two."},
	}

	for i, tc := range tcs {
		bodyStr := strings.Replace(tc.Body, "$$MESSAGE$$", tc.Message, -1)

		// in the case of a resume, we have to sub in our session body from our start
		bodyStr = strings.Replace(bodyStr, "$$SESSION$$", string(session), -1)

		var body io.Reader
		if tc.Body != "" {
			body = bytes.NewReader([]byte(bodyStr))
		}

		req, err := http.NewRequest(tc.Method, fmt.Sprintf("http://localhost:%d", rt.Config.InternalPort)+tc.URL, body)
		assert.NoError(t, err, "%d: error creating request", i)

		resp, err := http.DefaultClient.Do(req)
		assert.NoError(t, err, "%d: error making request", i)

		assert.Equal(t, tc.ExpectedStatus, resp.StatusCode, "%d: unexpected status", i)

		content, err := io.ReadAll(resp.Body)
		assert.NoError(t, err, "%d: error reading body", i)

		// if this was a success, save our session
		if resp.StatusCode == 200 {
			// save the session for use in a resume
			parsed := make(map[string]any)
			jsonx.MustUnmarshal(content, &parsed)
			session = jsonx.MustMarshal(parsed["session"])

			context, hasContext := parsed["context"]
			if hasContext {
				assert.Contains(t, context, "contact")
				assert.Contains(t, context, "globals")
			}
		}

		assert.Contains(t, string(content), tc.ExpectedResponse, "%d: did not find expected response content", i)
	}
}

func TestSimulateEmail(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()
	time.Sleep(time.Second)
	defer server.Stop()

	flow := testdb.InsertFlow(t, rt, testdb.Org1, []byte(`{
		"uuid": "4c1f6b77-6f4f-4c33-9a3a-7b0e2d3c4a11",
		"name": "Email",
		"spec_version": "13.1.0",
		"language": "eng",
		"type": "messaging",
		"nodes": [
			{
				"uuid": "8c3b2f5e-1d0a-4b6e-9f7c-2a1d3e4f5b60",
				"actions": [
					{
						"uuid": "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
						"type": "send_email",
						"addresses": ["bob@example.com"],
						"subject": "Hi",
						"body": "Hello"
					}
				],
				"exits": [{"uuid": "f0e1d2c3-b4a5-4968-8776-655443322110"}]
			}
		]
	}`))

	body := strings.Replace(startBody, "9de3663f-c5c5-4c92-9f45-ecbc09abcc85", string(flow.UUID), 1)

	simulate := func() []map[string]any {
		resp, err := http.Post(fmt.Sprintf("http://localhost:%d/mi/sim/start", rt.Config.InternalPort), "application/json", strings.NewReader(body))
		assert.NoError(t, err)
		assert.Equal(t, 200, resp.StatusCode)

		content, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)

		parsed := struct {
			Events []map[string]any `json:"events"`
		}{}
		jsonx.MustUnmarshal(content, &parsed)
		return parsed.Events
	}

	findEvent := func(evts []map[string]any, typ string) map[string]any {
		for _, e := range evts {
			if e["type"] == typ {
				return e
			}
		}
		return nil
	}

	rt.Config.SMTPServer = `smtp://foo:bar@example.com?from=foo%40example.com`

	// workspace without its own SMTP settings or the shared email feature gets an error event
	evts := simulate()
	assert.Nil(t, findEvent(evts, "email_sent"))
	if errEvt := findEvent(evts, "error"); assert.NotNil(t, errEvt) {
		assert.Equal(t, "workspace has no SMTP settings for sending email", errEvt["text"])
	}

	// once granted the feature, sending is faked
	rt.DB.MustExec(`UPDATE orgs_org SET features = '{"shared_email"}' WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	evts = simulate()
	assert.Nil(t, findEvent(evts, "error"))
	assert.NotNil(t, findEvent(evts, "email_sent"))
}
