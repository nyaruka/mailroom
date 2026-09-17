package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeURN(t *testing.T) {
	tcs := []struct {
		urn        urns.URN
		country    i18n.Country
		normalized urns.URN
		e164       bool
		err        string
	}{
		{"tel:+16055741111", i18n.NilCountry, "tel:+16055741111", true, ""},
		{"tel:+1 (605) 574 2222", i18n.NilCountry, "tel:+16055742222", true, ""},
		{"tel:16055742222", i18n.NilCountry, "tel:+16055742222", true, ""}, // long enough to try with a + prefix
		{"tel:+593979123456", "US", "tel:+593979123456", true, ""},         // explicit country code wins over org country
		{"tel:6055742222", "US", "tel:+16055742222", true, ""},             // local number parsed with org country
		{"tel:(605) 574-2222", "US", "tel:+16055742222", true, ""},
		{"tel:0788 383 383", "RW", "tel:+250788383383", true, ""},
		{"tel:0788 383 383", i18n.NilCountry, "tel:0788383383", false, ""}, // no country so left as a non-E164 number
		{"tel:0788 383 383", "US", "tel:+10788383383", true, ""},           // libphonenumber only checks possibility (length) for US
		{"tel:12", "US", "tel:12", false, ""},                              // too short to be a number in any country
		{"tel:1234", "US", "tel:1234", false, ""},                          // short codes are valid URNs but not E164
		{"tel:SENDER", "US", "tel:sender", false, ""},
		{"tel:+16055742222#Bob", "US", "tel:+16055742222#Bob", true, ""}, // display preserved
		{"tel:[[[", "US", "tel:[[[", false, "invalid path component"},
		{"tel:", "US", "tel:", false, "scheme or path cannot be empty"},
		{"twitter:@Bob", "US", "twitter:bob", false, ""},
		{"xyz:1234", "US", "xyz:1234", false, "unknown URN scheme"},
		{"abc", "US", "abc", false, "scheme or path cannot be empty"},
	}

	for _, tc := range tcs {
		normalized, e164, err := models.NormalizeURN(tc.urn, tc.country)

		assert.Equal(t, tc.normalized, normalized, "normalized mismatch for %s in %s", tc.urn, tc.country)
		assert.Equal(t, tc.e164, e164, "e164 mismatch for %s in %s", tc.urn, tc.country)
		if tc.err != "" {
			assert.EqualError(t, err, tc.err, "error mismatch for %s in %s", tc.urn, tc.country)
		} else {
			assert.NoError(t, err, "unexpected error for %s in %s", tc.urn, tc.country)
		}
	}
}
