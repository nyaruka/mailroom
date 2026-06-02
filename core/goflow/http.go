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

// HTTP returns the http.Client used by the engine for user-controlled webhook calls (flow
// call_webhook actions and resthook deliveries). When cfg.WebhookProxyURL is set, the client
// routes through that forward HTTP proxy; otherwise no proxy is used (the transport ignores
// HTTP_PROXY/HTTPS_PROXY env vars by design). The SSRF IP blocklist is applied to the client's
// transport as defense-in-depth, and requests are routed through the global httpx requestor so
// that tests can intercept them with mocks (see httpx.SetRequestor).
func HTTP(cfg *runtime.Config) *http.Client {
	httpInit.Do(func() {
		access := httpx.NewAccessConfig(10*time.Second, cfg.DisallowedIPs, cfg.DisallowedNets)

		httpClient = &http.Client{
			Transport: &requestorTransport{inner: httpx.WithAccessControl(newWebhookTransport(cfg), access)},
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

// requestorTransport routes requests through the current global httpx requestor (set via
// httpx.SetRequestor) so the engine's webhook calls can be intercepted by mocks in tests. In
// production the requestor is httpx.DefaultRequestor, which delegates to the inner transport.
type requestorTransport struct {
	inner http.RoundTripper
}

func (t *requestorTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// access control already lives on the inner transport, so pass nil here to avoid re-checking
	return httpx.Do(&http.Client{Transport: t.inner}, request, nil, nil)
}
