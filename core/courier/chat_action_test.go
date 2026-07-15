package courier_test

import (
	"io"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/courier"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendChatAction(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	rt.Config.CourierAuthToken = "sesame"

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://localhost:8080/ci/chat_action/send": {
			httpx.NewMockResponse(200, nil, []byte(`{"supported": true, "interval": 4}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"supported": false}`)),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
		},
	})
	rt.HTTP.Services.Transport = mocks

	oa := testdb.Org1.Load(t, rt)
	channel := oa.ChannelByUUID(testdb.FacebookChannel.UUID)
	require.NotNil(t, channel)

	resp, err := courier.SendChatAction(ctx, rt, channel, courier.ChatActionTypingStarted, urns.URN("facebook:12345"), "ex123")
	assert.NoError(t, err)
	assert.Equal(t, &courier.ChatActionResponse{Supported: true, Interval: 4}, resp)

	req := mocks.Requests()[0]
	assert.Equal(t, "Bearer sesame", req.Header.Get("Authorization"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	test.AssertEqualJSON(t, []byte(`{
		"action": "typing_started",
		"channel_type": "FBA",
		"channel_uuid": "0f661e8b-ea9d-4bd3-9953-d368340acf91",
		"urn": "facebook:12345",
		"msg_external_id": "ex123"
	}`), body)

	resp, err = courier.SendChatAction(ctx, rt, channel, courier.ChatActionTypingStarted, urns.URN("facebook:12345"), "ex123")
	assert.NoError(t, err)
	assert.Equal(t, &courier.ChatActionResponse{Supported: false}, resp)

	_, err = courier.SendChatAction(ctx, rt, channel, courier.ChatActionMarkRead, urns.URN("facebook:12345"), "ex123")
	assert.EqualError(t, err, `error calling courier endpoint, got non-200 status: {"error": "oops"}`)

	assert.False(t, mocks.HasUnused())
}
