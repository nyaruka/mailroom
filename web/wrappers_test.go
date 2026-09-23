package web_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteDeadline(t *testing.T) {
	// a handler which takes longer than the server's write timeout to respond
	slow := func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		time.Sleep(300 * time.Millisecond)
		return web.WriteMarshalled(w, http.StatusOK, map[string]string{"status": "ok"})
	}

	serve := func(handler web.Handler) *httptest.Server {
		svr := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, handler(r.Context(), nil, r, w))
		}))
		svr.Config.WriteTimeout = 100 * time.Millisecond
		svr.Start()
		return svr
	}

	// without the wrapper the response is never written and the connection is closed
	svr := serve(slow)
	defer svr.Close()

	resp, err := http.Get(svr.URL)
	if err == nil {
		resp.Body.Close()
	}
	assert.Error(t, err)

	// with the wrapper the handler gets its own deadline and the response is written
	svr = serve(web.WriteDeadline(time.Second, slow))
	defer svr.Close()

	resp, err = http.Get(svr.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"status": "ok"}`, string(body))
}
