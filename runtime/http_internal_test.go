package runtime

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookTransportWithProxyConfigured(t *testing.T) {
	cfg := NewDefaultConfig()
	u, err := url.Parse("http://proxy.example.com:3128")
	require.NoError(t, err)
	cfg.WebhookProxyURLParsed = u

	tr := newWebhookTransport(cfg)

	req, _ := http.NewRequest("POST", "https://example.org/hook", nil)
	proxy, err := tr.Proxy(req)
	require.NoError(t, err)
	require.NotNil(t, proxy, "transport should resolve a proxy URL when WebhookProxyURL is set")
	assert.Equal(t, "proxy.example.com:3128", proxy.Host)
	assert.Equal(t, "http", proxy.Scheme)
}

func TestWebhookTransportWithoutProxy(t *testing.T) {
	cfg := NewDefaultConfig()
	// WebhookProxyURLParsed is nil — transport must have nil Proxy regardless of host env vars
	tr := newWebhookTransport(cfg)

	assert.Nil(t, tr.Proxy)
}
