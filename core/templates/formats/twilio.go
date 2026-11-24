package formats

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/mailroom/v26/core/templates"
)

var twilioVariableRegex = regexp.MustCompile(`{{([A-Za-z0-9]+)}}`)

// TwilioContent is a template in Twilio's Content API format
type TwilioContent struct {
	SID          externalID      `json:"sid"`
	FriendlyName string          `json:"friendly_name"`
	Language     string          `json:"language"`
	Types        json.RawMessage `json:"types"`
	Links        struct {
		ApprovalFetch string `json:"approval_fetch"`
	} `json:"links"`
}

type twilioType struct {
	HeaderText   string          `json:"header_text"`
	Media        []string        `json:"media"`
	Body         string          `json:"body"`
	Footer       string          `json:"footer"`
	Title        string          `json:"title"`
	Subtitle     string          `json:"subtitle"`
	Items        json.RawMessage `json:"items"`
	DynamicItems json.RawMessage `json:"dynamic_items"`
	Actions      []*twilioAction `json:"actions"`
}

type twilioAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Phone string `json:"phone"`
}

// ParseTwilioContent parses a template in Twilio's format. Its approval status comes from a separate call so is
// passed in.
func ParseTwilioContent(c *TwilioContent, status templates.TranslationStatus) (*templates.Translation, error) {
	locale, err := ParseLanguage(c.Language)
	if err != nil {
		return nil, fmt.Errorf("error parsing template %s: %w", c.FriendlyName, err)
	}

	components, variables, supported, err := extractTwilioTypes(c.Types)
	if err != nil {
		return nil, fmt.Errorf("error parsing template %s: %w", c.FriendlyName, err)
	}

	return &templates.Translation{
		Name:           c.FriendlyName,
		Locale:         locale,
		ExternalLocale: c.Language,
		ExternalID:     string(c.SID),
		Namespace:      "",
		Status:         status,
		Components:     components,
		Variables:      variables,
		IsSupported:    supported,
	}, nil
}

func extractTwilioTypes(raw json.RawMessage) ([]*templates.Component, []*templates.Variable, bool, error) {
	components := make([]*templates.Component, 0)
	variables := make([]*templates.Variable, 0)
	supported := true

	// unlike Meta's format, variables with the same name in different components are the same variable
	shared := make(map[string]int)

	addVariables := func(names []string, typ string) map[string]int {
		m := make(map[string]int, len(names))
		for _, name := range names {
			if idx, ok := shared[name]; ok {
				m[name] = idx
			} else {
				variables = append(variables, &templates.Variable{Type: typ})
				m[name] = len(variables) - 1
				shared[name] = len(variables) - 1
			}
		}
		return m
	}
	addComponent := func(name, typ, content string) {
		vars := addVariables(extractVariables(twilioVariableRegex, content), "text")
		components = append(components, &templates.Component{
			Name:      name,
			Type:      typ,
			Content:   content,
			Variables: vars,
		})
	}

	// types is an object keyed by content type, and the order of its keys is the order of the components
	err := jsonparser.ObjectEach(raw, func(key, value []byte, dataType jsonparser.ValueType, _ int) error {
		contentType := string(key)

		if dataType != jsonparser.Object {
			return fmt.Errorf("content type %s isn't an object", contentType)
		}

		t := &twilioType{}
		if err := json.Unmarshal(value, t); err != nil {
			return fmt.Errorf("error unmarshaling content type %s: %w", contentType, err)
		}

		if t.HeaderText != "" {
			addComponent("header", "header/text", t.HeaderText)
		}
		if len(t.Media) > 0 {
			addComponent("header", "header/media", t.Media[0])
		}
		if t.Body != "" {
			addComponent("body", "body/text", t.Body)
		}
		if t.Footer != "" {
			addComponent("footer", "footer/text", t.Footer)
		}
		if t.Title != "" {
			addComponent("body", "body/text", t.Title)
		}
		if t.Subtitle != "" {
			addComponent("footer", "footer/text", t.Subtitle)
		}
		if isTruthy(t.Items) || isTruthy(t.DynamicItems) {
			supported = false
		}

		for i, a := range t.Actions {
			name := fmt.Sprintf("button.%d", i)
			display := a.Title

			if contentType == "twilio/quick-reply" {
				vars := addVariables(extractVariables(twilioVariableRegex, a.Title), "text")
				components = append(components, &templates.Component{
					Name:      name,
					Type:      "button/quick_reply",
					Content:   a.Title,
					Variables: vars,
				})
			} else if contentType == "twilio/call-to-action" || contentType == "twilio/card" || contentType == "whatsapp/card" {
				switch a.Type {
				case "URL":
					vars := addVariables(extractVariables(twilioVariableRegex, a.URL), "text")
					components = append(components, &templates.Component{
						Name:      name,
						Type:      "button/url",
						Content:   a.URL,
						Display:   &display,
						Variables: vars,
					})
				case "PHONE_NUMBER":
					components = append(components, &templates.Component{
						Name:      name,
						Type:      "button/phone_number",
						Content:   a.Phone,
						Display:   &display,
						Variables: map[string]int{},
					})
				case "QUICK_REPLY":
					vars := addVariables(extractVariables(twilioVariableRegex, a.Title), "text")
					components = append(components, &templates.Component{
						Name:      name,
						Type:      "button/quick_reply",
						Content:   a.Title,
						Variables: vars,
					})
				default:
					supported = false
				}
			} else {
				supported = false
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, false, err
	}

	return components, variables, supported && len(components) > 0, nil
}

// isTruthy returns whether a JSON value would be truthy in Python, e.g. a non-empty list
func isTruthy(v json.RawMessage) bool {
	switch string(v) {
	case "", "null", "false", "0", `""`, "[]", "{}":
		return false
	}
	return true
}
