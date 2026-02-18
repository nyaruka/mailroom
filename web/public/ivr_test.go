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
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/services/ivr/twiml"
	"github.com/nyaruka/mailroom/services/ivr/vonage"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/clogs"
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

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// start mocked API server
	mockTwilio := test.NewHTTPServer(50001, http.HandlerFunc(mockTwilioHandler))
	defer mockTwilio.Close()

	twiml.BaseURL = mockTwilio.URL
	twiml.IgnoreSignatures = true

	testdb.InsertIncomingCallTrigger(t, rt, testdb.Org1, testdb.IVRFlow, []*testdb.Group{testdb.DoctorsGroup}, nil, nil)

	// set callback domain and enable machine detection
	rt.DB.MustExec(`UPDATE channels_channel SET config = config || '{"callback_domain": "localhost:8091", "machine_detection": true}'::jsonb WHERE id = $1`, testdb.TwilioChannel.ID)

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
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-190b-76f8-92a3-d648ab64bccb' WHERE external_id = 'Call1'`)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-2c93-76f8-8f41-6b2d9f33d623' WHERE external_id = 'Call2'`)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-401b-76f8-ba00-bd7f0d08e671' WHERE external_id = 'Call3'`)

	testsuite.RunWebTests(t, rt, "./testdata/ivr_twilio.json")

	// check our final state of sessions, runs, msgs, calls
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'C'`, testdb.Ann.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE contact_id = $1 AND status = 'C'`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND status = 'W' AND direction = 'O'`, testdb.Ann.ID).Returns(10)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' AND status = 'H' AND direction = 'I'`, testdb.Ann.ID).Returns(4)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND msg_type = 'V' 
		AND ((status = 'H' AND direction = 'I') OR (status = 'W' AND direction = 'O'))`, testdb.Bob.ID).Returns(2)

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

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// deactivate our twilio channel
	rt.DB.MustExec(`UPDATE channels_channel SET is_active = FALSE WHERE id = $1`, testdb.TwilioChannel.ID)

	// update callback domain and role
	rt.DB.MustExec(`UPDATE channels_channel SET config = config || '{"callback_domain": "localhost:8091"}'::jsonb, role='SRCA' WHERE id = $1`, testdb.VonageChannel.ID)

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
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-190b-76f8-92a3-d648ab64bccb' WHERE external_id = 'Call1'`)
	rt.DB.MustExec(`UPDATE ivr_call SET uuid = '01969b47-2c93-76f8-8f41-6b2d9f33d623' WHERE external_id = 'Call2'`)

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

func getCallLogs(t *testing.T, rt *runtime.Runtime, ch *testdb.Channel) []*httpx.Log {
	rt.Dynamo.Main.Flush()

	var logUUIDs []clogs.UUID
	err := rt.DB.Select(&logUUIDs, `SELECT unnest(log_uuids) FROM ivr_call ORDER BY id`)
	require.NoError(t, err)

	logs := make([]*httpx.Log, 0, len(logUUIDs))

	type DataGZ struct {
		HttpLogs []*httpx.Log   `json:"http_logs"`
		Errors   []*clogs.Error `json:"errors"`
	}

	for _, logUUID := range logUUIDs {
		key := dynamo.Key{PK: fmt.Sprintf("cha#%s#%s", ch.UUID, logUUID[35:36]), SK: fmt.Sprintf("log#%s", logUUID)}
		item, err := dynamo.GetItem(t.Context(), rt.Dynamo.Main.Client(), "TestMain", key)
		require.NoError(t, err)
		require.NotNil(t, item, "log item not found for key %s", key)

		var dataGZ DataGZ
		err = dynamo.UnmarshalJSONGZ(item.DataGZ, &dataGZ)
		require.NoError(t, err)

		logs = append(logs, dataGZ.HttpLogs...)
	}

	return logs
}
