package svclogs

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/nyaruka/gocommon/httpx"
)

// TraceRequest sends req through a client whose transport is inner and captures a single Trace of the request
// and response. It's the composable replacement for the deprecated httpx.DoTrace used by service clients
// (airtime, IVR): it wraps inner in an httpx.TracesTransport for this one request and returns the
// resulting trace. Callers compose any extra behaviour (e.g. retries via httpx.WithRetries) into inner before
// passing it in — when a retrier is composed inside the tracer this way, Trace.Retries is populated with the
// number of retries performed. On a transport-level failure the trace still carries the request (with no
// response) and the underlying error is returned with http.Client.Do's *url.Error wrapper removed, matching
// what DoTrace surfaced.
func TraceRequest(inner http.RoundTripper, timeout time.Duration, req *http.Request) (*httpx.Trace, error) {
	tracer := httpx.WithTraces(inner)
	client := &http.Client{Transport: tracer, Timeout: timeout}

	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}

	var trace *httpx.Trace
	if traces := tracer.Traces(); len(traces) > 0 {
		trace = traces[len(traces)-1]
	}

	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
	}
	return trace, err
}
