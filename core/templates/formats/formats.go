// Package formats parses the template payloads of providers into translations. Parsers are pure: anything that needs
// a provider call, e.g. Twilio's approval status, is fetched by the service and passed in.
package formats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"golang.org/x/text/language"
)

var statuses = map[string]templates.TranslationStatus{
	"PENDING":   templates.StatusPending,
	"APPROVED":  templates.StatusApproved,
	"REJECTED":  templates.StatusRejected,
	"PAUSED":    templates.StatusPaused,
	"DISABLED":  templates.StatusDisabled,
	"IN_APPEAL": templates.StatusInAppeal,
}

// ParseStatus parses a provider's approval status. Statuses we don't store, e.g. DELETED or PENDING_DELETION, return
// false so the translation is treated as gone.
func ParseStatus(s string) (templates.TranslationStatus, bool) {
	status, ok := statuses[strings.ToUpper(s)]
	return status, ok
}

// ParseLanguage converts a provider language code, which can be alpha2 ('en'), alpha2 and country ('en_US') or alpha3
// ('fil'), to our locale format ('eng' or 'eng-US').
func ParseLanguage(code string) (i18n.Locale, error) {
	lang, country, _ := strings.Cut(code, "_")

	if len(lang) == 2 {
		base, err := language.ParseBase(lang)
		if err != nil {
			return "", fmt.Errorf("unrecognized language code: %s", code)
		}
		lang = base.ISO3()
	}

	return i18n.NewLocale(i18n.Language(lang), i18n.Country(country)), nil
}

// extractVariables returns the unique variable names in the given text, sorted as strings
func extractVariables(re *regexp.Regexp, text string) []string {
	names := make([]string, 0, 2)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	slices.Sort(names)
	return names
}

// externalID is an id which providers may send as a string or a number
type externalID string

func (i *externalID) UnmarshalJSON(d []byte) error {
	if bytes.Equal(d, []byte("null")) {
		return nil
	}
	if len(d) > 0 && d[0] == '"' {
		var s string
		if err := json.Unmarshal(d, &s); err != nil {
			return err
		}
		*i = externalID(s)
		return nil
	}
	*i = externalID(d)
	return nil
}
