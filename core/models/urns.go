package models

import (
	"net/url"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
)

// NormalizeURN normalizes the given URN in the context of the given country, so that a phone URN with a local number
// becomes E164 if it can be parsed as a number in that country. Returns the normalized URN, whether it's a phone URN
// with an E164 number (i.e. not a short code or sender ID), and any validation error. If a phone number can't be parsed
// the URN is normalized in the usual country-less way, so callers always get the best available canonical form.
func NormalizeURN(urn urns.URN, country i18n.Country) (urns.URN, bool, error) {
	scheme, path, query, display := urn.ToParts()

	if scheme == urns.Phone.Prefix {
		if number, err := urns.ParseNumber(path, country, false, false); err == nil {
			q, _ := url.ParseQuery(query)
			if norm, err := urns.NewFromParts(scheme, number, q, display); err == nil {
				return norm, true, nil
			}
		}
	}

	norm := urn.Normalize()
	return norm, false, norm.Validate()
}
