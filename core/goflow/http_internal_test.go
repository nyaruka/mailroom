package goflow

import (
	"net/http"
	"testing"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPWithProxyConfigured(t *testing.T) {
	t.Cleanup(Reset)
	Reset()

	cfg := runtime.NewDefaultConfig()
	cfg.WebhookProxyURL = "http://proxy.example.com:3128"

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
	cfg.WebhookProxyURL = ""

	client, _ := HTTP(cfg)

	req, _ := http.NewRequest("POST", "https://example.org/hook", nil)
	// transport.Proxy is the cloned default (ProxyFromEnvironment); with no env-var proxy and
	// no WebhookProxyURL configured, it must resolve to no proxy.
	proxy, err := client.Transport.(*http.Transport).Proxy(req)
	require.NoError(t, err)
	assert.Nil(t, proxy)
}

func TestHTTPInvalidProxyURLPanics(t *testing.T) {
	t.Cleanup(Reset)
	Reset()

	cfg := runtime.NewDefaultConfig()
	cfg.WebhookProxyURL = "://not a url"

	assert.Panics(t, func() {
		HTTP(cfg)
	})
}
