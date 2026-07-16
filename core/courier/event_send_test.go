package courier_test

import (
	"io"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/courier"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendEvent(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	rt.Config.CourierAuthToken = "sesame"

	defer test.MockUniverse()()

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://localhost:8080/ci/event/send": {
			httpx.NewMockResponse(200, nil, []byte(`{"supported": true, "interval": 4}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"supported": false}`)),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
		},
	})
	rt.HTTP.Services.Transport = mocks

	oa := testdb.Org1.Load(t, rt)
	channel := oa.ChannelByUUID(testdb.FacebookChannel.UUID)
	require.NotNil(t, channel)

	event := events.NewTypingStarted(events.DirectionOutgoing, assets.NewChannelReference(channel.UUID(), channel.Name()), urns.URN("facebook:12345"), "ex123")

	resp, err := courier.SendEvent(ctx, rt, channel, event)
	assert.NoError(t, err)
	assert.Equal(t, &courier.SendEventResponse{Supported: true, Interval: 4}, resp)

	req := mocks.Requests()[0]
	assert.Equal(t, "Bearer sesame", req.Header.Get("Authorization"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	test.AssertEqualJSON(t, []byte(`{
		"channel_type": "FBA",
		"event": {
			"uuid": "01969b47-0583-76f8-ae7f-f8b243c49ff5",
			"type": "typing_started",
			"created_on": "2025-05-04T12:30:46.123456789Z",
			"direction": "outgoing",
			"channel": {"uuid": "0f661e8b-ea9d-4bd3-9953-d368340acf91", "name": "Facebook"},
			"urn": "facebook:12345",
			"msg_external_id": "ex123"
		}
	}`), body)

	resp, err = courier.SendEvent(ctx, rt, channel, event)
	assert.NoError(t, err)
	assert.Equal(t, &courier.SendEventResponse{Supported: false}, resp)

	_, err = courier.SendEvent(ctx, rt, channel, event)
	assert.EqualError(t, err, `error calling courier endpoint, got non-200 status: {"error": "oops"}`)

	assert.False(t, mocks.HasUnused())
}
