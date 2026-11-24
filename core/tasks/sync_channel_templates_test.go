package tasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	_ "github.com/nyaruka/mailroom/v26/services/templates/dialog360"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestSyncChannelTemplates(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	rt.HTTP.Services.Transport, _ = testsuite.MockTransport(map[string][]*httpx.MockResponse{
		"https://waba.example.com/v1/configs/templates": {
			httpx.NewMockResponse(200, nil, []byte(`{"waba_templates": [{"name": "hello", "language": "en", "status": "approved", "components": [{"type": "BODY", "text": "Hi {{1}}"}]}]}`)),
			httpx.NewMockResponse(500, nil, []byte(`oops`)),
		},
	})

	ch := testdb.InsertChannel(t, rt, testdb.Org1, "D3C", "360dialog", "1234", []string{"whatsapp"}, "SR", map[string]any{"base_url": "https://waba.example.com", "auth_token": "sesame"})

	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.SyncChannelTemplates{ChannelID: ch.ID})
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM templates_templatetranslation WHERE channel_id = $1 AND locale = 'eng'`, ch.ID).Returns(1)

	// a failed fetch is recorded on the channel rather than failing the task
	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.SyncChannelTemplates{ChannelID: ch.ID})
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT sync_state->'templates'->>'failed' FROM channels_channel WHERE id = $1`, ch.ID).Returns("true")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM templates_templatetranslation WHERE channel_id = $1`, ch.ID).Returns(1)

	// channels without templates are ignored
	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.SyncChannelTemplates{ChannelID: testdb.TwilioChannel.ID})
	testsuite.FlushTasks(t, rt)
}
