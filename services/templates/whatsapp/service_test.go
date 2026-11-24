package whatsapp_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
	_ "github.com/nyaruka/mailroom/v26/services/templates/whatsapp"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	ctx := t.Context()
	cfg := &runtime.Config{WhatsappAdminSystemUserToken: "sesame"}
	channel := &models.Channel{UUID_: "e19b6e46-9d50-4a20-8c9e-2d0a1e4dc0a9", Type_: "WAC", Config_: models.Config{"wa_waba_id": "1234", "fb_namespace": "ns1"}}

	client, mocks := testsuite.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://graph.facebook.com/v25.0/1234/message_templates?limit=255": {
			httpx.NewMockResponse(400, nil, []byte(`{"error": {"message": "invalid token"}}`)),
			httpx.NewMockResponse(200, nil, []byte(`{
				"data": [
					{"id": "111", "name": "hello", "language": "en_US", "status": "APPROVED", "components": [{"type": "BODY", "text": "Hi {{1}}"}]},
					{"id": "222", "name": "hello", "language": "fr", "status": "DELETED", "components": [{"type": "BODY", "text": "Bonjour {{1}}"}]},
					{"id": "333", "name": "direct_send", "language": "en", "status": "APPROVED", "source": "AUTO_GENERATED", "components": [{"type": "BODY", "text": "Hi"}]}
				],
				"paging": {"next": "https://graph.facebook.com/v25.0/1234/message_templates?limit=255&after=abc"}
			}`)),
		},
		"https://graph.facebook.com/v25.0/1234/message_templates?limit=255&after=abc": {
			httpx.NewMockResponse(200, nil, []byte(`{
				"data": [
					{"id": "444", "name": "goodbye", "language": "es", "status": "PENDING", "namespace": "ns2", "components": [{"type": "BODY", "text": "Adios"}]},
					{"id": "555", "name": "weird", "language": "xx", "status": "APPROVED", "components": [{"type": "BODY", "text": "?"}]}
				]
			}`)),
		},
	})

	svc, err := templates.GetService(cfg, client, channel)
	require.NoError(t, err)

	// provider returns an error
	clog := models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	_, err = svc.FetchTemplates(ctx, clog)
	assert.EqualError(t, err, "received non-2XX response: 400")
	assert.Len(t, clog.HttpLogs, 1)

	clog = models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	translations, err := svc.FetchTemplates(ctx, clog)
	assert.NoError(t, err)
	assert.Equal(t, []string{"hello/eng-US/A/ns1/111", "goodbye/spa/P/ns2/444"}, summarize(translations))
	assert.Len(t, clog.HttpLogs, 2)
	assert.Equal(t, "Bearer sesame", mocks.Requests()[0].Header.Get("Authorization"))
	assert.NotContains(t, string(jsonx.MustMarshal(clog.HttpLogs)), "sesame")

	// unparseable templates are logged as errors
	assert.Len(t, clog.Errors, 1)
	assert.Equal(t, "error parsing template weird: unrecognized language code: xx", clog.Errors[0].Message)

	// need a WABA and a token
	_, err = templates.GetService(cfg, client, &models.Channel{Type_: "WAC", Config_: models.Config{}})
	assert.EqualError(t, err, "missing wa_waba_id in channel config")
	_, err = templates.GetService(&runtime.Config{}, client, channel)
	assert.EqualError(t, err, "missing WhatsApp admin system user token")
}

func summarize(trs []*templates.Translation) []string {
	s := make([]string, len(trs))
	for i, tr := range trs {
		s[i] = fmt.Sprintf("%s/%s/%s/%s/%s", tr.Name, tr.Locale, tr.Status, tr.Namespace, tr.ExternalID)
	}
	return s
}
