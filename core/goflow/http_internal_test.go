package goflow

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPWithProxyConfigured(t *testing.T) {
	t.Cleanup(Reset)
	Reset()

	cfg := runtime.NewDefaultConfig()
	u, err := url.Parse("http://proxy.example.com:3128")
	require.NoError(t, err)
	cfg.WebhookProxyURLParsed = u

	client, access := HTTP(cfg)
	require.NotNil(t, access)

	req, _ := http.NewRequest("POST", "https://example.org/hook", nil)
	proxy, err := client.Transport.(*http.Transport).Proxy(req)
	require.NoError(t, err)
	require.NotNil(t, proxy, "transport should resolve a proxy URL when WebhookProxyURL is set")
	assert.Equal(t, "proxy.example.com:3128", proxy.Host)
	assert.Equal(t, "http", proxy.Scheme)
}

func TestHTTPWithoutProxy(t *testing.T) {
	t.Cleanup(Reset)
	Reset()

	cfg := runtime.NewDefaultConfig()
	// WebhookProxyURLParsed is nil — transport must have nil Proxy regardless of host env vars
	client, _ := HTTP(cfg)

	assert.Nil(t, client.Transport.(*http.Transport).Proxy)
}
