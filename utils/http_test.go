package utils_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"
	"testing/iotest"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/v26/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundTripFunc adapts a function to an http.RoundTripper for tests
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDoTraced(t *testing.T) {
	const url = "https://example.com/thing"

	tracedClient := func(inner http.RoundTripper) *http.Client {
		return &http.Client{Transport: httpx.WithTraces(inner)}
	}
	mocks := func(body []byte) http.RoundTripper {
		return httpx.WithMocks(nil, map[string][]*httpx.MockResponse{url: {httpx.NewMockResponse(200, nil, body)}})
	}

	// the call's trace comes back with the response body captured into it
	req, _ := http.NewRequest("GET", url, nil)
	trace, resp, err := utils.DoTraced(tracedClient(mocks([]byte("hello"))), req)
	require.NoError(t, err)
	require.NotNil(t, trace)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, []byte("hello"), trace.ResponseBody)

	// each call gets only its own trace even though the client is shared
	client := tracedClient(httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		url: {httpx.NewMockResponse(200, nil, []byte("first")), httpx.NewMockResponse(200, nil, []byte("second"))},
	}))
	req, _ = http.NewRequest("GET", url, nil)
	_, _, err = utils.DoTraced(client, req)
	require.NoError(t, err)
	req, _ = http.NewRequest("GET", url, nil)
	trace, _, err = utils.DoTraced(client, req)
	require.NoError(t, err)
	assert.Equal(t, []byte("second"), trace.ResponseBody)

	// a body-read error is deferred onto the handed-back body by the tracing transport, so it's only the
	// draining here that surfaces it as the returned error
	errClient := tracedClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := io.MultiReader(bytes.NewReader([]byte("partial")), iotest.ErrReader(io.ErrUnexpectedEOF))
		return &http.Response{Status: "200 OK", StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(body)}, nil
	}))
	req, _ = http.NewRequest("GET", url, nil)
	_, _, err = utils.DoTraced(errClient, req)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)

	// a transport level failure returns the underlying error without http.Client.Do's *url.Error wrapper, and
	// still yields a trace carrying the request
	failing := tracedClient(httpx.WithMocks(nil, map[string][]*httpx.MockResponse{url: {httpx.MockConnectionError}}))
	req, _ = http.NewRequest("GET", url, nil)
	trace, _, err = utils.DoTraced(failing, req)
	assert.EqualError(t, err, "unable to connect to server")
	if assert.NotNil(t, trace) {
		assert.Nil(t, trace.Response)
	}
}
