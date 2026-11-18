package public_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/models"
	_ "github.com/nyaruka/mailroom/v26/core/runner/handlers"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/services/ivr/mnotify"
	"github.com/nyaruka/mailroom/v26/services/ivr/twiml"
	"github.com/nyaruka/mailroom/v26/services/ivr/vonage"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mocks the Twilio API
func mockTwilioHandler(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	slog.Info("test server called", "method", r.Method, "url", r.URL.String(), "form", r.Form)
	if strings.HasSuffix(r.URL.String(), "Calls.json") {
		to := r.Form.Get("To")
		switch to {
		case "+16055741111":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"sid": "Call1"}`))
		case "+16055742222":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"sid": "Call2"}`))
		case "+16055743333":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"sid": "Call3"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
	if strings.HasSuffix(r.URL.String(), "recording.mp3") {
		w.WriteHeader(http.StatusOK)
	}
}

func TestTwilioIVR(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	// start mocked API server
	mockTwilio := test.NewHTTPServer(50001, http.HandlerFunc(mockTwilioHandler))
	defer mockTwilio.Close()

	twiml.BaseURL = mockTwilio.URL
	twiml.IgnoreSignatures = true

	testdb.InsertIncomingCallTrigger(t, rt, testdb.Org1, testdb.IVRFlow, []*testdb.Group{testdb.DoctorsGroup}, nil, nil)

	// set callback domain and enable machine detection
	rt.DB.MustExec(`UPDATE channels_channel SET config = config || '{"callback_domain": "localhost:8190", "machine_detection": true}'::jsonb WHERE id = $1`, testdb.TwilioChannel.ID)

	// create a flow start for Ann, Bob, and Cat
	parentSummary := []byte(`{
		"flow": {"name": "IVR Flow", "uuid": "2f81d0ea-4d75-4843-9371-3f7465311cce"}, 
		"uuid": "8bc73097-ac57-47fb-82e5-184f8ec6dbef", 
		"status": "active", 
		"contact": {
			"uuid": "a393abc0-283d-4c9b-a1b3-641a035c34bf",
			"id": 10000, 
			"name": "Ann", 
			"status": "active",
			"urns": ["tel:+16055741111"], 
			"fields": {"gender": {"text": "F"}}, 
			"groups": [{"name": "Doctors", "uuid": "c153e265-f7c9-4539-9dbc-9b358714b638"}], 
			"timezone": "America/Los_Angeles", 
			"created_on": "2019-07-23T09:35:01.439614-07:00"
		}, 
		"results": {}
	}`)
	start := models.NewFlowStart(testdb.Org1.ID, models.StartTypeTrigger, testdb.IVRFlow.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID}).
		WithParentSummary(parentSummary)

	err := tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &tasks.StartFlow{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	// check our 3 contacts have 3 wired calls
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND external_id = $3`,
		testdb.Ann.ID, models.CallStatusWired, "Call1").Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND external_id = $3`,
		testdb.Bob.ID, models.CallStatusWired, "Call2").Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND external_id = $3`,
		testdb.Cat.ID, models.CallStatusWired, "Call3").Returns(1)

	// give calls known UUIDs
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-190b-76f8-92ed-42cbd11a03fd' WHERE external_id = 'Call1'`)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-2c93-76f8-b774-0a98171a0712' WHERE external_id = 'Call2'`)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-401b-76f8-aac5-d9d0ae409dbe' WHERE external_id = 'Call3'`)

	testsuite.RunWebTests(t, rt, "./testdata/ivr_twilio.json")

	// check our final state of sessions, runs, msgs, calls
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'C'`, testdb.Ann.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE contact_id = $1 AND status = 'C'`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND status = 'W' AND direction = 'O'`, testdb.Ann.ID).Returns(10)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND status = 'H' AND direction = 'I'`, testdb.Ann.ID).Returns(4)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' 
		AND ((status = 'H' AND direction = 'I') OR (status = 'W' AND direction = 'O'))`, testdb.Bob.ID).Returns(2)

	// voice messages in both directions are tallied
	assertvk.HGetAll(t, vc, "{tallies}:live", map[string]string{
		"1|channels/10000/voice:in@2025-05-04":  "5",
		"1|channels/10000/voice:out@2025-05-04": "11",
		"1|msgs/voice:in@2025-05-04":            "5",
		"1|msgs/voice:out@2025-05-04":           "11",
	})

	// check the generated channel logs
	logs := getCallLogs(t, rt, testdb.TwilioChannel)
	assert.Len(t, logs, 19)
	for _, log := range logs {
		assert.NotContains(t, string(jsonx.MustMarshal(log)), "sesame") // auth token redacted
	}
}

func mockVonageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("recording") != "" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{})
	} else {
		type CallForm struct {
			To []struct {
				Number string `json:"number"`
			} `json:"to"`
			Action string `json:"action,omitempty"`
		}
		body, _ := io.ReadAll(r.Body)
		form := &CallForm{}
		json.Unmarshal(body, form)
		slog.Info("test server called", "method", r.Method, "url", r.URL.String(), "body", body, "form", form)

		// end of a leg
		if form.Action == "transfer" {
			w.WriteHeader(http.StatusNoContent)
		} else if form.To[0].Number == "16055741111" {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{ "uuid": "Call1","status": "started","direction": "outbound","conversation_uuid": "Conversation1"}`))
		} else if form.To[0].Number == "16055743333" {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{ "uuid": "Call2","status": "started","direction": "outbound","conversation_uuid": "Conversation2"}`))
		} else if form.To[0].Number == "12065551212" {
			// start of a transfer leg
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{ "uuid": "Call3","status": "started","direction": "outbound","conversation_uuid": "Conversation3"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestVonageIVR(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	// deactivate our twilio channel
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.TwilioChannel.ID)

	// update callback domain and role
	rt.DB.MustExec(`UPDATE channels_channel SET config = config || '{"callback_domain": "localhost:8190"}'::jsonb, role='SRCA' WHERE id = $1`, testdb.VonageChannel.ID)

	// start mocked API server
	mockVonage := test.NewHTTPServer(50002, http.HandlerFunc(mockVonageHandler))
	defer mockVonage.Close()

	vonage.CallURL = mockVonage.URL
	vonage.IgnoreSignatures = true

	// create a flow start for Ann and Cat
	start := models.NewFlowStart(testdb.Org1.ID, models.StartTypeTrigger, testdb.IVRFlow.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Cat.ID}).
		WithParams(json.RawMessage(`{"ref_id":"123"}`))

	err := models.InsertFlowStart(ctx, rt.DB, start)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM flows_flowstart`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM flows_flowstart WHERE params ->> 'ref_id' = '123'`).Returns(1)

	err = tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &tasks.StartFlow{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND external_id = $3`,
		testdb.Ann.ID, models.CallStatusWired, "Call1").Returns(1)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE contact_id = $1 AND status = $2 AND external_id = $3`,
		testdb.Cat.ID, models.CallStatusWired, "Call2").Returns(1)

	// give calls known UUIDs
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-190b-76f8-92ed-42cbd11a03fd' WHERE external_id = 'Call1'`)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-2c93-76f8-b774-0a98171a0712' WHERE external_id = 'Call2'`)

	testsuite.RunWebTests(t, rt, "./testdata/ivr_vonage.json")

	// check our final state of sessions, runs, calls, msgs
	assertdb.Query(t, rt.DB, `SELECT format('%s/%s', contact_uuid, status), count(*) FROM flows_flowsession GROUP BY 1`).
		Map(map[string]any{"a393abc0-283d-4c9b-a1b3-641a035c34bf/C": 1, "cd024bcd-f473-4719-a00a-bd0bb1190135/W": 1})
	assertdb.Query(t, rt.DB, `SELECT format('%s/%s', contact_id, status), count(*) FROM flows_flowrun GROUP BY 1`).
		Map(map[string]any{"10000/C": 1, "10002/W": 1})
	assertdb.Query(t, rt.DB, `SELECT format('%s/%s', contact_id, status), count(*) FROM ivr_call GROUP BY 1`).
		Map(map[string]any{"10000/D": 1, "10002/D": 1, "30000/F": 1})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND status = 'W' AND direction = 'O'`, testdb.Ann.ID).Returns(9)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND status = 'H' AND direction = 'I'`, testdb.Ann.ID).Returns(5)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND ((status = 'H' AND direction = 'I') OR (status = 'W' AND direction = 'O'))`, testdb.Cat.ID).Returns(3)

	// check the generated channel logs
	logs := getCallLogs(t, rt, testdb.VonageChannel)
	assert.Len(t, logs, 16)
	for _, log := range logs {
		assert.NotContains(t, string(jsonx.MustMarshal(log)), "BEGIN PRIVATE KEY") // private key redacted
	}
}

func TestMNotifyIVR(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// deactivate our twilio channel and add an mNotify channel
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.TwilioChannel.ID)

	channel := testdb.InsertChannel(t, rt, testdb.Org1, "MN", "mNotify", "+16055550000", []string{"tel"}, "CA", map[string]any{
		"api_key":         "key123",
		"secret":          "sesame",
		"callback_domain": "localhost:8190",
	})
	rt.DB.MustExec(`UPDATE channels_channel SET uuid = 'a2b5d3cf-8a0c-4a0f-9a35-6a1e0d0f7b11' WHERE id = $1`, channel.ID)
	channel.UUID = "a2b5d3cf-8a0c-4a0f-9a35-6a1e0d0f7b11"

	// start mocked API server
	mockMNotify := test.NewHTTPServer(50003, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		slog.Info("test server called", "method", r.Method, "url", r.URL.String(), "body", body)

		assert.Equal(t, "key123", r.URL.Query().Get("key"))
		w.Write([]byte(`{"success": true, "message": "New record created successfully"}`))
	}))
	defer mockMNotify.Close()

	mnotify.BaseURL = mockMNotify.URL

	testdb.InsertIncomingCallTrigger(t, rt, testdb.Org1, testdb.IVRFlow, []*testdb.Group{testdb.DoctorsGroup}, nil, nil)

	// create a flow start for Ann and Bob
	start := models.NewFlowStart(testdb.Org1.ID, models.StartTypeTrigger, testdb.IVRFlow.ID).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID})

	err := tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &tasks.StartFlow{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	// both have wired calls which until they're answered have our UUID as their external id
	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE status = 'W' AND external_id = uuid::text AND channel_id = $1`, channel.ID).Returns(2)

	// give calls known UUIDs
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-190b-76f8-92ed-42cbd11a03fd', external_id = '01969b47-190b-76f8-92ed-42cbd11a03fd' WHERE contact_id = $1`, testdb.Ann.ID)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-2c93-76f8-b774-0a98171a0712', external_id = '01969b47-2c93-76f8-b774-0a98171a0712' WHERE contact_id = $1`, testdb.Bob.ID)

	// and a call on another channel that callbacks to this one shouldn't be able to touch
	other := testdb.InsertCall(t, rt, testdb.Org1, testdb.VonageChannel, testdb.Dan)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-401b-76f8-aac5-d9d0ae409dbe', external_id = 'Other1' WHERE id = $1`, other.ID)

	testsuite.RunWebTests(t, rt, "./testdata/ivr_mnotify.json")

	// the test flow has no audio so its responses are never more than one action - queue up some more for Ann's
	// call as if they'd come from a sprint, including one more than should ever be reached
	vc := rt.VK.Get()
	defer vc.Close()

	_, err = vc.Do("RPUSH", "mnotify:actions:01969b47-190b-76f8-92ed-42cbd11a03fd",
		`{"actions":{"playback":{"url":"https://example.com/2.wav"}}}`,
		`{"actions":{"get_digits":{"url":"https://example.com/3.wav","num_digits":1}}}`,
		`{"actions":{"playback":{"url":"https://example.com/4.wav"}}}`,
	)
	require.NoError(t, err)

	testsuite.RunWebTests(t, rt, "./testdata/ivr_mnotify_queued.json")

	// what was queued for a call that ended is left alone
	assertvk.LLen(t, vc, "mnotify:actions:01969b47-190b-76f8-92ed-42cbd11a03fd", 1)

	// wire calls to Cat and Dan, and then block Dan
	start = models.NewFlowStart(testdb.Org1.ID, models.StartTypeTrigger, testdb.IVRFlow.ID).
		WithContactIDs([]models.ContactID{testdb.Cat.ID, testdb.Dan.ID})

	err = tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &tasks.StartFlow{FlowStart: start}, false)
	require.NoError(t, err)

	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ivr_call WHERE status = 'W' AND channel_id = $1`, channel.ID).Returns(2)

	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-5c2b-76f8-8d1e-3b7f6a1c9e20', external_id = 'Wired1' WHERE status = 'W' AND contact_id = $1`, testdb.Cat.ID)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-6f43-76f8-a3c4-91d2e8b7f055', external_id = 'Wired2' WHERE status = 'W' AND contact_id = $1`, testdb.Dan.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET status = 'B' WHERE id = $1`, testdb.Dan.ID)

	testsuite.RunWebTests(t, rt, "./testdata/ivr_mnotify_late.json")

	// check the generated channel logs
	logs := getCallLogs(t, rt, channel)
	for _, log := range logs {
		assert.NotContains(t, string(jsonx.MustMarshal(log)), "key123") // API key redacted
		assert.NotContains(t, string(jsonx.MustMarshal(log)), "sesame") // callback secret redacted
	}
}

func getCallLogs(t *testing.T, rt *runtime.Runtime, ch *testdb.Channel) []*httpx.Log {
	rt.Dynamo.Main.Flush()

	var logUUIDs []svclogs.UUID
	err := rt.DB.Select(&logUUIDs, `SELECT unnest(log_uuids) FROM ivr_call ORDER BY id`)
	require.NoError(t, err)

	logs := make([]*httpx.Log, 0, len(logUUIDs))

	type DataGZ struct {
		HttpLogs []*httpx.Log     `json:"http_logs"`
		Errors   []*svclogs.Error `json:"errors"`
	}

	for _, logUUID := range logUUIDs {
		key := dynamo.Key{PK: fmt.Sprintf("cha#%s#%s", ch.UUID, logUUID[35:36]), SK: fmt.Sprintf("log#%s", logUUID)}
		item, err := dynamo.GetItem(t.Context(), rt.Dynamo.Main.Client(), rt.Dynamo.Main.Table(), key)
		require.NoError(t, err)
		require.NotNil(t, item, "log item not found for key %s", key)

		var dataGZ DataGZ
		err = dynamo.UnmarshalJSONGZ(item.DataGZ, &dataGZ)
		require.NoError(t, err)

		logs = append(logs, dataGZ.HttpLogs...)
	}

	return logs
}
