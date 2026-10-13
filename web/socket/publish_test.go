package socket_test

import (
	"testing"

	"github.com/nyaruka/gocommon/centrifugo"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublish(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// a contact with no URNs has no route for typing indicators
	testdb.InsertContact(t, rt, testdb.Org1, "f5e5c595-0cba-4eb9-b1e6-41d7f7f0add6", "Mr Unreachable", "eng", models.ContactStatusActive)

	// mocks consumed in order by the test cases that get as far as forwarding to courier
	transport, mocks := testsuite.MockTransport(map[string][]*httpx.MockResponse{
		"http://localhost:8080/ci/event/send": {
			httpx.NewMockResponse(200, nil, []byte(`{"supported": true, "interval": 4}`)),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"supported": false}`)),
		},
	})
	rt.HTTP.Services.Transport = transport

	// a webchat channel, with a fixed UUID as its sockets are named by it
	wch := testdb.InsertChannel(t, rt, testdb.Org1, models.ChannelTypeWebChat, "Web Chat", "", []string{"webchat"}, "SR", map[string]any{})
	rt.DB.MustExec(`UPDATE channels_channel SET uuid = '8c3e4d2a-5b1f-4e6a-9c7d-2f0b1a3e5d47' WHERE id = $1`, wch.ID)

	insertVisitor := func(uuid core.ContactUUID, chatID string, channel *testdb.Channel) *testdb.Contact {
		contact := testdb.InsertContact(t, rt, testdb.Org1, uuid, "", "", models.ContactStatusActive)
		urnID := testdb.InsertContactURN(t, rt, testdb.Org1, contact, urns.URN("webchat:"+chatID), 1000, nil)
		rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $2 WHERE id = $1`, urnID, channel.ID)
		return contact
	}

	visitor := insertVisitor("c5a1e1f7-3b4d-4f2e-8a6c-9d0b7e2f4a13", "AbCdEf0123456789GhIjKlMn", wch)
	insertVisitor("a13c5a1e-1f73-4b4d-8f2e-8a6c9d0b7e2f", "Throttled012345678901234", wch)
	insertVisitor("0b7e2f4a-9d0b-4f2e-8a6c-c5a1e1f73b4d", "ZyXwVu9876543210TsRqPoNm", testdb.TwilioChannel)
	gone := insertVisitor("4f2e8a6c-c5a1-4e1f-b3b4-9d0b7e2f4a13", "InActive0123456789abcdef", wch)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, gone.ID)

	vc := rt.VK.Get()
	defer vc.Close()

	// an agent has the visitor's history open, and one chat has already used up its typing allowance
	historySocket := models.HistorySocket(visitor.UUID)
	_, err := vc.Do("SET", centrifugo.SubscriptionKey(historySocket), "1")
	require.NoError(t, err)
	_, err = vc.Do("SET", "chat-typing:8c3e4d2a-5b1f-4e6a-9c7d-2f0b1a3e5d47|Throttled012345678901234", 30, "EX", 60)
	require.NoError(t, err)

	testsuite.RunWebTests(t, rt, "testdata/publish.json")

	assert.False(t, mocks.HasUnused())

	// each accepted visitor publication reached the agent as an incoming event routed via the webchat channel
	sent := testsuite.CentrifugoHistory(t, rt, historySocket)
	if assert.Len(t, sent, 3) {
		test.AssertEqualJSON(t, []byte(`{
			"uuid": "01969b47-24c3-76f8-b20c-e3cb6203e029",
			"type": "typing_started",
			"created_on": "2025-05-04T12:30:54.123456789Z",
			"direction": "incoming",
			"channel": {"uuid": "8c3e4d2a-5b1f-4e6a-9c7d-2f0b1a3e5d47", "name": "Web Chat"},
			"urn": "webchat:AbCdEf0123456789GhIjKlMn"
		}`), sent[0], "unexpected history publication")
	}
}

func TestPublishToDisabledChannel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wch := testdb.InsertChannel(t, rt, testdb.Org1, models.ChannelTypeWebChat, "Web Chat", "", []string{"webchat"}, "SR", map[string]any{})
	rt.DB.MustExec(`UPDATE channels_channel SET uuid = '8c3e4d2a-5b1f-4e6a-9c7d-2f0b1a3e5d47' WHERE id = $1`, wch.ID)
	contact := testdb.InsertContact(t, rt, testdb.Org1, "c5a1e1f7-3b4d-4f2e-8a6c-9d0b7e2f4a13", "", "", models.ContactStatusActive)
	urnID := testdb.InsertContactURN(t, rt, testdb.Org1, contact, urns.URN("webchat:AbCdEf0123456789GhIjKlMn"), 1000, nil)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $2 WHERE id = $1`, urnID, wch.ID)

	// the visitor is cached, then their channel is disabled, which org assets see first
	v, err := models.GetWebChatVisitor(ctx, "8c3e4d2a-5b1f-4e6a-9c7d-2f0b1a3e5d47", "AbCdEf0123456789GhIjKlMn")
	require.NoError(t, err)
	require.NotNil(t, v)

	rt.DB.MustExec(`UPDATE channels_channel SET is_enabled = FALSE WHERE id = $1`, wch.ID)
	_, err = models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshChannels)
	require.NoError(t, err)

	testsuite.RunWebTests(t, rt, "testdata/publish_disabled_channel.json")
}
