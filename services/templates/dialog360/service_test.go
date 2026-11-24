package dialog360_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
	_ "github.com/nyaruka/mailroom/v26/services/templates/dialog360"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	ctx := t.Context()
	channel := &models.Channel{UUID_: "e19b6e46-9d50-4a20-8c9e-2d0a1e4dc0a9", Type_: "D3C", Config_: models.Config{"base_url": "https://waba-v2.360dialog.io/", "auth_token": "sesame"}}

	client, mocks := testsuite.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://waba-v2.360dialog.io/v1/configs/templates": {
			httpx.NewMockResponse(401, nil, []byte(`{"meta": {"success": false}}`)),
			httpx.NewMockResponse(200, nil, []byte(`{
				"waba_templates": [
					{"name": "hello", "language": "en", "status": "approved", "namespace": "ns1", "components": [{"type": "BODY", "text": "Hi {{1}}"}]},
					{"name": "direct_send", "language": "en", "status": "approved", "source": "AUTO_GENERATED", "components": [{"type": "BODY", "text": "Hi"}]},
					{"name": "goodbye", "language": "pt_BR", "status": "rejected", "components": [{"type": "BODY", "text": "Tchau"}]}
				]
			}`)),
		},
	})

	svc, err := templates.GetService(&runtime.Config{}, client, channel)
	require.NoError(t, err)

	clog := models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	_, err = svc.FetchTemplates(ctx, clog)
	assert.EqualError(t, err, "received non-2XX response: 401")

	clog = models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	translations, err := svc.FetchTemplates(ctx, clog)
	assert.NoError(t, err)
	assert.Equal(t, []string{"hello/eng/A/ns1", "goodbye/por-BR/R/"}, summarize(translations))
	assert.Len(t, clog.HttpLogs, 1)
	assert.Equal(t, "sesame", mocks.Requests()[0].Header.Get("D360-API-KEY"))
	assert.NotContains(t, string(jsonx.MustMarshal(clog.HttpLogs)), "sesame")

	_, err = templates.GetService(&runtime.Config{}, client, &models.Channel{Type_: "D3C", Config_: models.Config{"base_url": "https://waba-v2.360dialog.io"}})
	assert.EqualError(t, err, "missing base_url or auth_token in channel config")
}

func summarize(trs []*templates.Translation) []string {
	s := make([]string, len(trs))
	for i, tr := range trs {
		s[i] = fmt.Sprintf("%s/%s/%s/%s", tr.Name, tr.Locale, tr.Status, tr.Namespace)
	}
	return s
}
