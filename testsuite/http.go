package testsuite

import (
	"net/http"

	"github.com/nyaruka/gocommon/httpx"
)

// MockTransport returns a transport serving the given mocks wrapped in tracing, along with the mocking transport
// itself so tests can assert on what was requested. Tests install the former on runtime.HTTP.Services' Transport in
// place of the real stack, and the tracing has to stay in the stack: it's what captures the traces that
// utils.DoTraced hands back for attaching to a channel log, so a bare mocking transport would leave every log empty
// - and would do so without failing to compile.
func MockTransport(mocks map[string][]*httpx.MockResponse) (http.RoundTripper, *httpx.MocksTransport) {
	transport := httpx.WithMocks(http.DefaultTransport, mocks)
	return httpx.WithTraces(transport), transport
}

// MockedHTTP returns an http.Client equivalent to runtime.HTTP.Services - i.e. carrying the same tracing - but
// serving the given mocks, along with the mocking transport itself. Use it for a service which is constructed with
// its own client rather than handed the runtime's.
func MockedHTTP(mocks map[string][]*httpx.MockResponse) (*http.Client, *httpx.MocksTransport) {
	traced, transport := MockTransport(mocks)
	return &http.Client{Transport: traced}, transport
}
