package socket_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
)

func TestPublish(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	// mocks consumed in order by the test cases that get as far as forwarding to courier
	mocks := httpx.WithMocks(http.DefaultTransport, map[string][]*httpx.MockResponse{
		"http://localhost:8080/ci/event/send": {
			httpx.NewMockResponse(200, nil, []byte(`{"supported": true, "interval": 4}`)),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
		},
	})
	rt.HTTP.Services.Transport = mocks

	testsuite.RunWebTests(t, rt, "testdata/publish.json")

	assert.False(t, mocks.HasUnused())
}
