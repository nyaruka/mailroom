package runtime

import (
	"crypto/tls"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/httpx"
)

// HTTP holds the http.Clients used for outbound calls. Services is for fixed third-party APIs (LLM providers,
// airtime, IVR, courier). Engine and Simulator are for the user-controlled webhook calls made by the flow
// engine and simulator respectively — those apply the SSRF IP blocklist and route through the configured
// webhook proxy. Tests can replace a client's Transport with a mocking transport to intercept its calls.
type HTTP struct {
	Services  *http.Client
	Engine    *http.Client
	Simulator *http.Client
}

func newHTTP(cfg *Config) *HTTP {
	return &HTTP{
		Services:  newServicesClient(cfg),
		Engine:    newWebhookClient(cfg),
		Simulator: newWebhookClient(cfg),
	}
}

// newServicesClient builds the client for fixed third-party service APIs. It does not apply the SSRF blocklist
// or the webhook proxy — those are reserved for user-controlled webhook URLs.
func newServicesClient(cfg *Config) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 32
	t.MaxIdleConnsPerHost = 8
	t.IdleConnTimeout = 30 * time.Second
	t.TLSClientConfig = &tls.Config{
		Renegotiation: tls.RenegotiateOnceAsClient,
	}
	return &http.Client{
		Transport: t,
		Timeout:   time.Duration(cfg.WebhooksTimeout) * time.Millisecond,
	}
}

// newWebhookClient builds an http.Client for user-controlled webhook calls (flow call_webhook actions and
// resthook deliveries). The SSRF IP blocklist is applied to its transport as defense-in-depth, and when
// cfg.WebhookProxyURL is set requests route through that forward proxy. The outer transport runs a full
// client.Do on the held inner client so the inner client follows redirects itself — a tracing wrapper layered
// on top (by the engine's webhook service) therefore sees only the final hop, not intermediate 3xx responses.
// Tests can replace the returned client's Transport (e.g. with httpx.WithMocks) to intercept webhook calls.
func newWebhookClient(cfg *Config) *http.Client {
	access := httpx.NewAccessConfig(10*time.Second, cfg.DisallowedIPs, cfg.DisallowedNets)
	inner := &http.Client{Transport: httpx.WithAccessControl(newWebhookTransport(cfg), access)}
	return &http.Client{
		Transport: &webhookTransport{client: inner},
		Timeout:   time.Duration(cfg.WebhooksTimeout) * time.Millisecond,
	}
}

// newWebhookTransport builds the base transport for webhook calls, honoring the configured proxy. When
// cfg.WebhookProxyURL is set the transport routes through that forward proxy; otherwise no proxy is used (env
// vars like HTTP_PROXY/HTTPS_PROXY are deliberately ignored).
func newWebhookTransport(cfg *Config) *http.Transport {
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

// webhookTransport runs a full client.Do on the held client (so the inner client follows redirects itself)
// rather than a plain transport round trip. In production the held client carries the access-controlled
// transport; tests replace the webhook client's Transport entirely to intercept calls, bypassing this wrapper.
type webhookTransport struct {
	client *http.Client
}

func (t *webhookTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.client.Do(request)
}
