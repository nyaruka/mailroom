package socket_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestPublish(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	// Ann's newest incoming message is where her typing indicators get routed (Bob has no incoming messages)
	msgIn := testdb.InsertIncomingMsg(t, rt, testdb.Org1, "0199bad8-d4be-76c7-8a5c-a12caae7aa87", testdb.FacebookChannel, testdb.Ann, "hi there", models.MsgStatusHandled, "")
	rt.DB.MustExec(`UPDATE msgs_msg SET external_identifier = 'EX123' WHERE id = $1`, msgIn.ID)

	// mocks consumed in order by the test cases that get as far as forwarding to courier
	mocks := httpx.WithMocks(http.DefaultTransport, map[string][]*httpx.MockResponse{
		"http://localhost:8080/ci/event/send": {
			httpx.NewMockResponse(200, nil, []byte(`{"supported": true, "interval": 4}`)),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"supported": false}`)),
		},
	})
	rt.HTTP.Services.Transport = mocks

	testsuite.RunWebTests(t, rt, "testdata/publish.json")

	assert.False(t, mocks.HasUnused())
}
