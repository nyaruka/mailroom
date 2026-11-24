// Package turn syncs the templates of Turn.io WhatsApp channels, which are fetched from the Graph API with the
// channel's own Facebook credentials.
package turn

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/core/templates/formats"
	"github.com/nyaruka/mailroom/v26/runtime"
)

const (
	channelType = models.ChannelType("TRN")

	configBusinessID   = "fb_business_id"
	configAccessToken  = "fb_access_token"
	configNamespace    = "fb_namespace"
	configDomain       = "fb_template_list_domain"
	configAPIVersion   = "fb_template_list_domain_api_version"
	configUsername     = "username"
	configPassword     = "password"
	defaultDomain      = "graph.facebook.com"
	defaultAPIVersion  = "v14.0"
	templatesURLFormat = "https://%s/%s/%s/message_templates"
)

func init() {
	templates.RegisterService(channelType, New)
}

type service struct {
	httpClient  *http.Client
	domain      string
	url         string
	accessToken string
	namespace   string
}

// New creates a new templates service for the given Turn.io channel
func New(cfg *runtime.Config, httpClient *http.Client, channel *models.Channel) (templates.Service, error) {
	c := channel.Config()
	businessID := c.GetString(configBusinessID, "")
	accessToken := c.GetString(configAccessToken, "")
	if businessID == "" || accessToken == "" {
		return nil, fmt.Errorf("missing %s or %s in channel config", configBusinessID, configAccessToken)
	}

	// channels set up before the domain and version were configurable use the defaults
	domain := c.GetString(configDomain, defaultDomain)
	version := c.GetString(configAPIVersion, defaultAPIVersion)

	return &service{
		httpClient:  httpClient,
		domain:      domain,
		url:         fmt.Sprintf(templatesURLFormat, domain, version, url.PathEscape(businessID)),
		accessToken: accessToken,
		namespace:   c.GetString(configNamespace, ""),
	}, nil
}

func (s *service) FetchTemplates(ctx context.Context, clog *models.ChannelLog) (*templates.Fetched, error) {
	next := s.url + "?" + url.Values{"access_token": {s.accessToken}, "limit": {"255"}}.Encode()
	raws := make([]*formats.MetaTemplate, 0)

	for next != "" {
		if err := templates.CheckHost(next, s.domain); err != nil {
			return nil, err
		}

		body, err := templates.Get(ctx, s.httpClient, clog, next, nil)
		if err != nil {
			return nil, err
		}

		page := &formats.MetaPage{}
		if err := json.Unmarshal(body, page); err != nil {
			return nil, fmt.Errorf("error unmarshaling templates: %w", err)
		}

		raws = append(raws, page.Data...)
		next = page.Paging.Next
	}

	return formats.ParseMetaTemplates(raws, s.namespace), nil
}

func (s *service) RedactValues(ch *models.Channel) []string {
	vals := []string{s.accessToken}

	// the channel's courier credentials aren't used here but are redacted from all of its logs
	username := ch.Config().GetString(configUsername, "")
	password := ch.Config().GetString(configPassword, "")
	if password != "" {
		vals = append(vals, password, httpx.BasicAuth(username, password))
	}
	return vals
}
