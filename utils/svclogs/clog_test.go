package svclogs_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/utils/svclogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogs(t *testing.T) {
	ctx := context.Background()

	mocks := httpx.WithMocks(http.DefaultTransport, map[string][]*httpx.MockResponse{
		"http://ivr.com/start":  {httpx.NewMockResponse(200, nil, []byte("OK"))},
		"http://ivr.com/hangup": {httpx.NewMockResponse(400, nil, []byte("Oops"))},
	})

	clog1 := svclogs.New("type1", nil, []string{"sesame"})
	clog2 := svclogs.New("type1", nil, []string{"sesame"})

	req1, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/start", nil, map[string]string{"Authorization": "Token sesame"})
	trace1, err := svclogs.TraceRequest(mocks, 0, req1)
	require.NoError(t, err)

	clog1.HTTP(trace1)
	clog1.End()

	req2, _ := httpx.NewRequest(ctx, "GET", "http://ivr.com/hangup", nil, nil)
	trace2, err := svclogs.TraceRequest(mocks, 0, req2)
	require.NoError(t, err)

	clog2.HTTP(trace2)
	clog2.Error(&svclogs.Error{Message: "oops"})
	clog2.End()

	assert.NotEqual(t, clog1.UUID, clog2.UUID)
	assert.NotEqual(t, time.Duration(0), clog1.Elapsed)
}
