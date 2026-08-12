package utils

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/nyaruka/gocommon/httpx"
)

// DoTraced performs req through the given client and returns the trace of that single call alongside the response.
// The client's transport must be wrapped with httpx.WithTraces, as runtime.HTTP.Services is; a collector is put on
// the request context so that only this call's trace comes back even though the client is shared.
//
// The response body is drained before returning. The tracing transport buffers the body into the trace and defers any
// read error onto the handed-back body rather than returning it, so a caller which takes its bytes from the trace -
// which is what all of ours do - would otherwise accept a truncated or short read as though it were complete.
// Draining here surfaces that as the returned error instead, and is the reason to go through this rather than calling
// client.Do directly. It does mean the returned response is good for its status and headers only - its body has been
// read to the end and closed, so take the bytes from the trace's ResponseBody.
//
// A transport-level failure is returned with http.Client.Do's *url.Error wrapper removed, so that the message which
// ends up in a service log is the underlying error rather than one prefixed with the method and full URL.
func DoTraced(client *http.Client, req *http.Request) (*httpx.Trace, *http.Response, error) {
	ctx, traces := httpx.WithTraceCollector(req.Context())

	resp, err := client.Do(req.WithContext(ctx))

	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
	}

	if resp != nil {
		if _, drainErr := io.Copy(io.Discard, resp.Body); drainErr != nil && err == nil {
			err = drainErr
		}
		resp.Body.Close()
	}

	return traces.Last(), resp, err
}
