package public

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoggable(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://mailroom.io/mr/ivr/c/1234/callback?token=sesame&action=playback", nil)

	logged := fmt.Sprintf("%+v", loggable(r, []string{"key123", "", "sesame"}))
	assert.NotContains(t, logged, "sesame")
	assert.Contains(t, logged, "token=****&action=playback")

	// original request is unchanged
	assert.Equal(t, "sesame", r.URL.Query().Get("token"))
}
