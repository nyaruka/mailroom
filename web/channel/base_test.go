package channel_test

import (
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	_ "github.com/nyaruka/mailroom/v26/services/templates/dialog360"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestInterrupt(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/interrupt.json")
}

func TestSyncTemplates(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	rt.HTTP.Services.Transport, _ = testsuite.MockTransport(map[string][]*httpx.MockResponse{
		"https://waba.example.com/v1/configs/templates": {
			httpx.NewMockResponse(401, nil, []byte(`{"meta": {"success": false}}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"waba_templates": [
				{"name": "hello", "language": "en", "status": "approved", "components": [{"type": "BODY", "text": "Hi {{1}}"}]},
				{"name": "hello", "language": "fr", "status": "pending", "components": [{"type": "BODY", "text": "Bonjour {{1}}"}]}
			]}`)),
		},
	})

	// give our channel an id the test cases can use
	ch := testdb.InsertChannel(t, rt, testdb.Org1, "D3C", "360dialog", "1234", []string{"whatsapp"}, "SR", map[string]any{"base_url": "https://waba.example.com", "auth_token": "sesame"})
	rt.DB.MustExec(`UPDATE channels_channel SET id = 30000 WHERE id = $1`, ch.ID)

	testsuite.RunWebTests(t, rt, "testdata/sync_templates.json")
}
