package runtime

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
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

func TestWebhookTransportDisablesKeepAlivesWithProxy(t *testing.T) {
	cfg := NewDefaultConfig()

	// without a proxy, connections are pooled as normal
	assert.False(t, newWebhookTransport(cfg).DisableKeepAlives, "keepalives should be enabled when not proxying")

	// with a proxy, keepalives are disabled to avoid the stale-reuse race against the proxy
	u, err := url.Parse("http://proxy.example.com:3128")
	require.NoError(t, err)
	cfg.WebhookProxyURLParsed = u
	assert.True(t, newWebhookTransport(cfg).DisableKeepAlives, "keepalives should be disabled when proxying")
}

func TestWebhookRetries(t *testing.T) {
	cfg := NewDefaultConfig()

	// disabled when retries or backoff are zero
	cfg.WebhooksMaxRetries = 0
	assert.Nil(t, webhookRetries(cfg))
	cfg.WebhooksMaxRetries = 2
	cfg.WebhooksInitialBackoff = 0
	assert.Nil(t, webhookRetries(cfg))

	// configured otherwise, scoped to connection errors (nil response)
	cfg.WebhooksInitialBackoff = 1
	r := webhookRetries(cfg)
	require.NotNil(t, r)
	assert.Equal(t, 2, r.MaxRetries())
	assert.True(t, r.ShouldRetry(nil, nil, 0), "should retry when no response (connection error)")
	assert.False(t, r.ShouldRetry(nil, &http.Response{StatusCode: 500}, 0), "should not retry once the origin responded")
}

// flakyTransport returns a connection error (nil response) for its first failures calls, then delegates.
type flakyTransport struct {
	failures int
	calls    int
	inner    http.RoundTripper
}

func (t *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls <= t.failures {
		return nil, errors.New("connection reset")
	}
	return t.inner.RoundTrip(req)
}

func TestWebhookClientRetriesConnectionErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := NewDefaultConfig()
	cfg.WebhooksInitialBackoff = 1 // keep the test fast
	retries := webhookRetries(cfg)

	// connection errors below the retry allowance recover to success
	flaky := &flakyTransport{failures: 2, inner: http.DefaultTransport}
	rt := httpx.WithRetries(flaky, retries)
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 3, flaky.calls, "should have retried twice before succeeding")

	// a 5xx response is not retried — the origin saw the request
	five := &countingTransport{resp: &http.Response{StatusCode: 500, Body: http.NoBody, Header: http.Header{}}}
	rt = httpx.WithRetries(five, retries)
	req, _ = http.NewRequest("POST", "http://example.org/hook", nil)
	resp, err = rt.RoundTrip(req)
	require.NoError(t, err)
	assert.Equal(t, 500, resp.StatusCode)
	assert.Equal(t, 1, five.calls, "a response (even 5xx) must not be retried")
}

// countingTransport always returns the same response and counts calls.
type countingTransport struct {
	resp  *http.Response
	calls int
}

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return t.resp, nil
}
