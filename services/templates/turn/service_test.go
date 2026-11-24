package turn_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
	_ "github.com/nyaruka/mailroom/v26/services/templates/turn"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	ctx := t.Context()
	config := models.Config{"fb_business_id": "1234", "fb_access_token": "sesame", "fb_namespace": "ns1", "username": "bob", "password": "pass123"}
	channel := &models.Channel{UUID_: "e19b6e46-9d50-4a20-8c9e-2d0a1e4dc0a9", Type_: "TRN", Config_: config}

	client, _ := testsuite.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://graph.facebook.com/v14.0/1234/message_templates?access_token=sesame&limit=255": {
			httpx.NewMockResponse(500, nil, []byte(`oops`)),
			httpx.NewMockResponse(200, nil, []byte(`{
				"data": [{"name": "hello", "language": "en", "status": "APPROVED", "components": [{"type": "BODY", "text": "Hi {{1}}"}]}],
				"paging": {"next": "https://graph.facebook.com/v14.0/1234/message_templates?access_token=sesame&limit=255&after=abc"}
			}`)),
		},
		"https://graph.facebook.com/v14.0/1234/message_templates?access_token=sesame&limit=255&after=abc": {
			httpx.NewMockResponse(200, nil, []byte(`{"data": [{"name": "goodbye", "language": "sw", "status": "PAUSED", "components": [{"type": "BODY", "text": "Kwaheri"}]}]}`)),
		},
		"https://turn.example.com/v16.0/1234/message_templates?access_token=sesame&limit=255": {
			httpx.NewMockResponse(200, nil, []byte(`{"data": [], "paging": {"next": "https://graph.facebook.com/v16.0/1234/message_templates?after=abc"}}`)),
		},
	})

	svc, err := templates.GetService(&runtime.Config{}, client, channel)
	require.NoError(t, err)

	clog := models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	_, err = svc.FetchTemplates(ctx, clog)
	assert.EqualError(t, err, "received non-2XX response: 500")

	clog = models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	fetched, err := svc.FetchTemplates(ctx, clog)
	assert.NoError(t, err)
	assert.Equal(t, []string{"hello/eng/A/ns1", "goodbye/swa/U/ns1"}, summarize(fetched.Translations))
	assert.Len(t, clog.HttpLogs, 2)
	assert.NotContains(t, string(jsonx.MustMarshal(clog.HttpLogs)), "sesame")
	assert.Equal(t, []string{"sesame", "pass123", "Ym9iOnBhc3MxMjM="}, svc.RedactValues(channel))

	// the domain and API version can be configured
	config["fb_template_list_domain"] = "turn.example.com"
	config["fb_template_list_domain_api_version"] = "v16.0"
	svc, err = templates.GetService(&runtime.Config{}, client, channel)
	require.NoError(t, err)

	// and we only follow pages on that domain
	_, err = svc.FetchTemplates(ctx, models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, nil))
	assert.EqualError(t, err, "provider returned URL not on turn.example.com: https://graph.facebook.com/v16.0/1234/message_templates?after=abc")

	_, err = templates.GetService(&runtime.Config{}, client, &models.Channel{Type_: "TRN", Config_: models.Config{"fb_business_id": "1234"}})
	assert.EqualError(t, err, "missing fb_business_id or fb_access_token in channel config")
}

func summarize(trs []*templates.Translation) []string {
	s := make([]string, len(trs))
	for i, tr := range trs {
		s[i] = fmt.Sprintf("%s/%s/%s/%s", tr.Name, tr.Locale, tr.Status, tr.Namespace)
	}
	return s
}
