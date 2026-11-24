package templates

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/utils"
)

// ServiceConstructor defines our signature for creating a new templates service from a channel
type ServiceConstructor func(*runtime.Config, *http.Client, *models.Channel) (Service, error)

var registeredTypes = make(map[models.ChannelType]ServiceConstructor)

// RegisterService registers a new templates service for the given channel type
func RegisterService(channelType models.ChannelType, constructor ServiceConstructor) {
	registeredTypes[channelType] = constructor
}

// HasService returns whether the given channel type has a templates service
func HasService(channelType models.ChannelType) bool {
	return registeredTypes[channelType] != nil
}

// GetService creates the right kind of templates service for the passed in channel. As with IVR services, the
// httpClient's transport is used for the service's provider calls so tests can mock them.
func GetService(cfg *runtime.Config, httpClient *http.Client, channel *models.Channel) (Service, error) {
	constructor := registeredTypes[channel.Type()]
	if constructor == nil {
		return nil, fmt.Errorf("no registered templates service for channel type: %s", channel.Type())
	}

	return constructor(cfg, httpClient, channel)
}

// Service is what each provider implements to fetch a channel's templates
type Service interface {
	// FetchTemplates fetches all of the channel's templates, recording each provider call on the log
	FetchTemplates(ctx context.Context, clog *models.ChannelLog) (*Fetched, error)

	RedactValues(*models.Channel) []string
}

// TranslationStatus is the provider's approval status of a translation
type TranslationStatus string

const (
	StatusPending  TranslationStatus = "P"
	StatusApproved TranslationStatus = "A"
	StatusRejected TranslationStatus = "R"
	StatusPaused   TranslationStatus = "U"
	StatusDisabled TranslationStatus = "D"
	StatusInAppeal TranslationStatus = "I"

	// StatusUnknown is for a translation whose status couldn't be fetched, which keeps its stored status, or is
	// pending if new
	StatusUnknown TranslationStatus = ""
)

// Fetched is what a service fetched for a channel
type Fetched struct {
	Translations []*Translation

	// templates that couldn't be parsed, whose stored translations are left as they are rather than deleted
	Invalid []*InvalidTemplateError
}

// InvalidTemplateError is a template from a provider that we can't store
type InvalidTemplateError struct {
	Name string
	Err  error
}

func (e *InvalidTemplateError) Error() string {
	return fmt.Sprintf("invalid template %s: %s", e.Name, e.Err)
}
func (e *InvalidTemplateError) Unwrap() error { return e.Err }

// Translation is a template translation as fetched from a provider, normalized into what we store
type Translation struct {
	Name           string
	Locale         i18n.Locale
	ExternalLocale string
	ExternalID     string
	Namespace      string
	Status         TranslationStatus
	Components     []*Component
	Variables      []*Variable
	IsSupported    bool
}

// Component is a part of a translation, e.g. its body or a button
type Component struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Content string  `json:"content"`
	Display *string `json:"display,omitempty"` // only buttons have one, but they have one even when it's empty

	// maps the names of the variables in Content to their indexes in the translation's variables
	Variables map[string]int `json:"variables"`
}

// Variable is a parameter of a translation
type Variable struct {
	Type string `json:"type"`
}

// Get makes a GET request to a provider, recording it on the channel log, and returns the response body. A non-2XX
// response is an error.
func Get(ctx context.Context, client *http.Client, clog *models.ChannelLog, u string, headers map[string]string) (
	[]byte, error,
) {
	req, err := httpx.NewRequest(ctx, http.MethodGet, u, nil, headers)
	if err != nil {
		return nil, err
	}

	trace, resp, err := utils.DoTraced(client, req)
	if trace != nil {
		clog.HTTP(trace)
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("received non-2XX response: %d", resp.StatusCode)
	}

	return trace.ResponseBody, nil
}

// CheckHost returns an error if a URL we've been given by a provider, e.g. for its next page of templates, isn't on
// the expected host, since requests to it carry the channel's credentials
func CheckHost(rawURL, host string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host != host {
		return fmt.Errorf("provider returned URL not on %s: %s", host, rawURL)
	}
	return nil
}
