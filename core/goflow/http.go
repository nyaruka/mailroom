package goflow

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/runtime"
)

var httpInit sync.Once

var httpClient *http.Client

// webhookMockTransport, when non-nil, intercepts the engine's webhook calls so tests can mock outbound HTTP
// without hitting the network. The engine and its webhook client are process-global (built once), so this seam
// is global too — install it via SetWebhookMockTransport. It replaces the previous global httpx requestor.
var webhookMockTransport http.RoundTripper

// SetWebhookMockTransport installs (or clears, when nil) a transport that intercepts the engine's webhook calls.
// Intended for tests: pass an httpx.WithMocks transport to mock responses, or nil to make real calls.
func SetWebhookMockTransport(transport http.RoundTripper) {
	webhookMockTransport = transport
}

// HTTP returns the http.Client used by the engine for user-controlled webhook calls (flow
// call_webhook actions and resthook deliveries). When cfg.WebhookProxyURL is set, the client
// routes through that forward HTTP proxy; otherwise no proxy is used (the transport ignores
// HTTP_PROXY/HTTPS_PROXY env vars by design). The SSRF IP blocklist is applied to the client's
// transport as defense-in-depth, and webhook calls pass through any transport installed via
// SetWebhookMockTransport so that tests can intercept them with mocks.
func HTTP(cfg *runtime.Config) *http.Client {
	httpInit.Do(func() {
		access := httpx.NewAccessConfig(10*time.Second, cfg.DisallowedIPs, cfg.DisallowedNets)

		// inner is the client used in production; access control lives on its transport so that an installed
		// mock transport (which short-circuits inner) bypasses it
		inner := &http.Client{Transport: httpx.WithAccessControl(newWebhookTransport(cfg), access)}

		httpClient = &http.Client{
			Transport: &mockableTransport{client: inner},
			Timeout:   time.Duration(cfg.WebhooksTimeout) * time.Millisecond,
		}
	})
	return httpClient
}

// newWebhookTransport builds the base transport for webhook calls, honoring the configured proxy. When
// cfg.WebhookProxyURL is set the transport routes through that forward proxy; otherwise no proxy is used (env vars
// like HTTP_PROXY/HTTPS_PROXY are deliberately ignored).
func newWebhookTransport(cfg *runtime.Config) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 32
	t.MaxIdleConnsPerHost = 8
	t.IdleConnTimeout = 30 * time.Second
	t.TLSClientConfig = &tls.Config{
		Renegotiation: tls.RenegotiateOnceAsClient, // support single TLS renegotiation
	}

	if cfg.WebhookProxyURLParsed != nil {
		t.Proxy = http.ProxyURL(cfg.WebhookProxyURLParsed)
	} else {
		t.Proxy = nil
	}

	return t
}

// mockableTransport delegates webhook requests to the held client, unless a test has installed a mock transport
// via SetWebhookMockTransport — in which case the request is served from that instead, short-circuiting the inner
// client (and so its access control and redirect handling). In production no mock is installed and it just runs
// the inner client.
//
// It runs a full client.Do inside RoundTrip rather than a plain transport round trip so that the inner client
// follows redirects itself; a tracing wrapper layered on top of this transport therefore only sees the final hop,
// not intermediate 3xx responses.
type mockableTransport struct {
	client *http.Client
}

func (t *mockableTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if webhookMockTransport != nil {
		return webhookMockTransport.RoundTrip(request)
	}
	// access control already lives on the client's transport, so a plain client.Do is enough
	return t.client.Do(request)
}
