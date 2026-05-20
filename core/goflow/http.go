package goflow

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/runtime"
)

var httpInit sync.Once

var httpClient *http.Client
var httpAccess *httpx.AccessConfig

// HTTP returns the http.Client and SSRF access config used by the engine for user-controlled
// webhook calls (flow call_webhook actions and resthook deliveries). When cfg.WebhookProxyURL
// is set, the client routes through that forward HTTP proxy; the SSRF IP blocklist is still
// applied as defense-in-depth.
func HTTP(cfg *runtime.Config) (*http.Client, *httpx.AccessConfig) {
	httpInit.Do(func() {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxIdleConns = 32
		t.MaxIdleConnsPerHost = 8
		t.IdleConnTimeout = 30 * time.Second
		t.TLSClientConfig = &tls.Config{
			Renegotiation: tls.RenegotiateOnceAsClient, // support single TLS renegotiation
		}

		if cfg.WebhookProxyURL != "" {
			u, err := url.Parse(cfg.WebhookProxyURL)
			if err != nil {
				panic(fmt.Errorf("invalid WebhookProxyURL %q: %w", cfg.WebhookProxyURL, err))
			}
			t.Proxy = http.ProxyURL(u)
		}

		httpClient = &http.Client{
			Transport: t,
			Timeout:   time.Duration(cfg.WebhooksTimeout) * time.Millisecond,
		}

		httpAccess = httpx.NewAccessConfig(10*time.Second, cfg.DisallowedIPs, cfg.DisallowedNets)
	})
	return httpClient, httpAccess
}
