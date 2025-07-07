package goflow

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/runtime"
)

var httpInit sync.Once

var httpClient *http.Client
var httpAccess *httpx.AccessConfig

// HTTP returns the configuration objects for HTTP calls from the engine and its services
func HTTP(cfg *runtime.Config) (*http.Client, *httpx.AccessConfig) {
	httpInit.Do(func() {
		// customize the default golang transport
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.MaxIdleConns = 32
		t.MaxIdleConnsPerHost = 8
		t.IdleConnTimeout = 30 * time.Second
		t.TLSClientConfig = &tls.Config{
			Renegotiation: tls.RenegotiateOnceAsClient, // support single TLS renegotiation
		}

		httpClient = &http.Client{
			Transport: t,
			Timeout:   time.Duration(cfg.WebhooksTimeout) * time.Millisecond,
		}

		disallowedIPs, disallowedNets, _ := cfg.ParseDisallowedNetworks()
		httpAccess = httpx.NewAccessConfig(10*time.Second, disallowedIPs, disallowedNets)
	})
	return httpClient, httpAccess
}
