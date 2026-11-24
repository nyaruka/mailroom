// Package twilio syncs the templates of Twilio WhatsApp channels from Twilio's Content API.
package twilio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/svclogs"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/core/templates/formats"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	channelType = models.ChannelType("TWA")

	configAccountSID = "account_sid"
	configAuthToken  = "auth_token"
)

// BaseURL is the Content API, a variable so tests can change it
var BaseURL = "https://content.twilio.com/v1"

func init() {
	templates.RegisterService(channelType, New)
}

type service struct {
	httpClient *http.Client
	host       string
	authToken  string
	headers    map[string]string
}

// New creates a new templates service for the given Twilio WhatsApp channel
func New(cfg *runtime.Config, httpClient *http.Client, channel *models.Channel) (templates.Service, error) {
	accountSID := channel.Config().GetString(configAccountSID, "")
	authToken := channel.Config().GetString(configAuthToken, "")
	if accountSID == "" || authToken == "" {
		return nil, fmt.Errorf("missing %s or %s in channel config", configAccountSID, configAuthToken)
	}

	base, err := url.Parse(BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	return &service{
		httpClient: httpClient,
		host:       base.Host,
		authToken:  authToken,
		headers:    map[string]string{"Authorization": "Basic " + httpx.BasicAuth(accountSID, authToken)},
	}, nil
}

func (s *service) FetchTemplates(ctx context.Context, clog *models.ChannelLog) (*templates.Fetched, error) {
	next := BaseURL + "/Content"
	contents := make([]*formats.TwilioContent, 0)

	for next != "" {
		if err := templates.CheckHost(next, s.host); err != nil {
			return nil, err
		}

		body, err := templates.Get(ctx, s.httpClient, clog, next, s.headers)
		if err != nil {
			return nil, err
		}

		var page struct {
			Contents []*formats.TwilioContent `json:"contents"`
			Meta     struct {
				NextPageURL string `json:"next_page_url"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("error unmarshaling templates: %w", err)
		}

		contents = append(contents, page.Contents...)
		next = page.Meta.NextPageURL
	}

	fetched := &templates.Fetched{Translations: make([]*templates.Translation, 0, len(contents))}

	for _, c := range contents {
		status, ok := s.fetchStatus(ctx, clog, c)
		if !ok {
			continue
		}

		tr, err := formats.ParseTwilioContent(c, status)
		if err != nil {
			fetched.Invalid = append(fetched.Invalid, err.(*templates.InvalidTemplateError))
		} else {
			fetched.Translations = append(fetched.Translations, tr)
		}
	}

	return fetched, nil
}

// fetchStatus fetches the WhatsApp approval status of a template, returning false if it's one we don't store. If it
// can't be fetched, the status is unknown so the stored one is kept, rather than failing the sync.
func (s *service) fetchStatus(ctx context.Context, clog *models.ChannelLog, c *formats.TwilioContent) (
	templates.TranslationStatus, bool,
) {
	err := templates.CheckHost(c.Links.ApprovalFetch, s.host)

	var body []byte
	if err == nil {
		body, err = templates.Get(ctx, s.httpClient, clog, c.Links.ApprovalFetch, s.headers)
	}
	if err != nil {
		clog.Error(&svclogs.Error{Message: fmt.Sprintf("error fetching approval of template %s: %s", c.FriendlyName, err)})
		return templates.StatusUnknown, true
	}

	var approval struct {
		WhatsApp struct {
			Status string `json:"status"`
		} `json:"whatsapp"`
	}
	if err := json.Unmarshal(body, &approval); err != nil || approval.WhatsApp.Status == "" {
		return templates.StatusPending, true
	}

	return formats.ParseStatus(approval.WhatsApp.Status)
}

func (s *service) RedactValues(*models.Channel) []string {
	return []string{s.authToken, s.headers["Authorization"][len("Basic "):]}
}
