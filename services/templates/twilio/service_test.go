package twilio_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
	_ "github.com/nyaruka/mailroom/v26/services/templates/twilio"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func content(sid, name, lang string) string {
	return fmt.Sprintf(`{"sid": "%s", "friendly_name": "%s", "language": "%s", "types": {"twilio/text": {"body": "Hi {{1}}"}}, "links": {"approval_fetch": "https://content.twilio.com/v1/Content/%s/ApprovalRequests"}}`, sid, name, lang, sid)
}

func TestService(t *testing.T) {
	ctx := t.Context()
	channel := &models.Channel{UUID_: "e19b6e46-9d50-4a20-8c9e-2d0a1e4dc0a9", Type_: "TWA", Config_: models.Config{"account_sid": "AC123", "auth_token": "sesame"}}

	client, mocks := testsuite.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://content.twilio.com/v1/Content": {
			httpx.NewMockResponse(400, nil, []byte(`{"meta": {"success": false}}`)),
			httpx.NewMockResponse(200, nil, []byte(`{
				"contents": [`+content("HX1", "hello", "en")+`, `+content("HX2", "goodbye", "es")+`],
				"meta": {"next_page_url": "https://content.twilio.com/v1/Content?PageSize=50&Page=1"}
			}`)),
		},
		"https://content.twilio.com/v1/Content?PageSize=50&Page=1": {
			httpx.NewMockResponse(200, nil, []byte(`{
				"contents": [`+content("HX3", "deleted", "en")+`, `+content("HX4", "unknown", "en")+`, {"sid": "HX5", "friendly_name": "elsewhere", "language": "en", "types": {"twilio/text": {"body": "Hi"}}, "links": {"approval_fetch": "https://evil.example.com/HX5"}}],
				"meta": {"next_page_url": null}
			}`)),
		},
		"https://content.twilio.com/v1/Content/HX1/ApprovalRequests": {httpx.NewMockResponse(200, nil, []byte(`{"whatsapp": {"status": "approved"}}`))},
		"https://content.twilio.com/v1/Content/HX2/ApprovalRequests": {httpx.NewMockResponse(500, nil, []byte(`oops`))},
		"https://content.twilio.com/v1/Content/HX3/ApprovalRequests": {httpx.NewMockResponse(200, nil, []byte(`{"whatsapp": {"status": "deleted"}}`))},
		"https://content.twilio.com/v1/Content/HX4/ApprovalRequests": {httpx.NewMockResponse(200, nil, []byte(`{}`))},
	})

	svc, err := templates.GetService(&runtime.Config{}, client, channel)
	require.NoError(t, err)

	clog := models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	_, err = svc.FetchTemplates(ctx, clog)
	assert.EqualError(t, err, "received non-2XX response: 400")

	// a template whose approval can't be fetched has an unknown status, one with no WhatsApp status is pending, and one
	// with a status we don't store is left out. Approvals are only fetched from Twilio.
	clog = models.NewChannelLog(models.ChannelLogTypeTemplateSync, channel, svc.RedactValues(channel))
	fetched, err := svc.FetchTemplates(ctx, clog)
	assert.NoError(t, err)
	assert.Equal(t, []string{"hello/eng/A/HX1", "goodbye/spa//HX2", "unknown/eng/P/HX4", "elsewhere/eng//HX5"}, summarize(fetched.Translations))
	assert.Len(t, clog.HttpLogs, 6)
	assert.Len(t, clog.Errors, 2)
	assert.Equal(t, "error fetching approval of template goodbye: received non-2XX response: 500", clog.Errors[0].Message)
	assert.Equal(t, "error fetching approval of template elsewhere: provider returned URL not on content.twilio.com: https://evil.example.com/HX5", clog.Errors[1].Message)
	assert.Equal(t, "Basic QUMxMjM6c2VzYW1l", mocks.Requests()[1].Header.Get("Authorization"))
	assert.NotContains(t, string(jsonx.MustMarshal(clog.HttpLogs)), "sesame")
	assert.NotContains(t, string(jsonx.MustMarshal(clog.HttpLogs)), "QUMxMjM6c2VzYW1l")
	assert.False(t, mocks.HasUnused())

	_, err = templates.GetService(&runtime.Config{}, client, &models.Channel{Type_: "TWA", Config_: models.Config{"account_sid": "AC123"}})
	assert.EqualError(t, err, "missing account_sid or auth_token in channel config")
}

func summarize(trs []*templates.Translation) []string {
	s := make([]string, len(trs))
	for i, tr := range trs {
		s[i] = fmt.Sprintf("%s/%s/%s/%s", tr.Name, tr.Locale, tr.Status, tr.ExternalID)
	}
	return s
}
