package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// blank-imported so TestListeners has both a registered internal and internet route to probe
	_ "github.com/nyaruka/mailroom/v26/web/contact"
	_ "github.com/nyaruka/mailroom/v26/web/public"
)

func TestServer(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/server.json")
}

// TestListeners verifies that internet and internal endpoints are correctly split
// between the two listener ports.
func TestListeners(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()
	defer server.Stop()

	for _, addr := range []string{fmt.Sprintf("localhost:%d", rt.Config.InternetPort), fmt.Sprintf("localhost:%d", rt.Config.InternalPort)} {
		require.Eventually(t, func() bool {
			c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err != nil {
				return false
			}
			c.Close()
			return true
		}, 5*time.Second, 10*time.Millisecond, "listener at %s never came up", addr)
	}

	internetURL := fmt.Sprintf("http://localhost:%d", rt.Config.InternetPort)
	internalURL := fmt.Sprintf("http://localhost:%d", rt.Config.InternalPort)

	// don't follow redirects so we can assert on the 301 from /mr/docs
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	tcs := []struct {
		label  string
		method string
		url    string
		status int
		body   string
	}{
		// internet listener: health at /, internet routes — no /mi/* routes
		{"internet: health", "GET", internetURL + "/", 200, `{"component": "mailroom", "listener": "internet", "version": "Dev"}`},
		{"internet: internet route", "GET", internetURL + "/mr/docs", 301, ""},
		{"internet: internal route not exposed", "GET", internetURL + "/mi/contact/parse_query", 404, ""},
		{"internet: unknown path", "GET", internetURL + "/nope", 404, ""},

		// internal listener: health at /, /mi/* routes — no /mr/*
		{"internal: health", "GET", internalURL + "/", 200, `{"component": "mailroom", "listener": "internal", "version": "Dev"}`},
		{"internal: internal route via GET (wrong method)", "GET", internalURL + "/mi/contact/parse_query", 405, ""},
		{"internal: internet route not exposed", "GET", internalURL + "/mr/docs", 404, ""},
		{"internal: unknown path", "GET", internalURL + "/nope", 404, ""},
	}

	for _, tc := range tcs {
		req, err := http.NewRequest(tc.method, tc.url, nil)
		require.NoError(t, err, tc.label)

		resp, err := client.Do(req)
		require.NoError(t, err, tc.label)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, tc.label)
		resp.Body.Close()

		assert.Equal(t, tc.status, resp.StatusCode, tc.label)
		if tc.body != "" {
			assert.JSONEq(t, tc.body, string(body), tc.label)
		}
	}
}

func TestWrapHandler(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)

	logs := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(logs))
	defer slog.SetDefault(prev)

	slowQuery := server.WrapHandler(func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		_, err := rt.DB.ExecContext(ctx, `SELECT pg_sleep(5)`)
		return err
	})
	slowFailure := server.WrapHandler(func(ctx context.Context, rt *runtime.Runtime, r *http.Request, w http.ResponseWriter) error {
		time.Sleep(300 * time.Millisecond)
		return errors.New("boom")
	})
	serve := func(handler http.HandlerFunc, ctx context.Context) int {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
		return rec.Code
	}

	// a request whose client goes away mid-query fails, but isn't an error of ours to report
	reqCtx, cancel := context.WithCancel(ctx)
	time.AfterFunc(200*time.Millisecond, cancel)
	assert.Equal(t, http.StatusInternalServerError, serve(slowQuery, reqCtx))
	assert.Equal(t, []string{"error handling request"}, logs.messages(slog.LevelDebug))
	assert.Empty(t, logs.messages(slog.LevelError))

	// whereas one that fails for some other reason after its client has gone is
	reqCtx, cancel = context.WithCancel(ctx)
	time.AfterFunc(100*time.Millisecond, cancel)
	assert.Equal(t, http.StatusInternalServerError, serve(slowFailure, reqCtx))
	assert.Equal(t, []string{"error handling request"}, logs.messages(slog.LevelError))

	// as is one that times out
	reqCtx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	assert.Equal(t, http.StatusInternalServerError, serve(slowQuery, reqCtx))
	assert.Equal(t, []string{"error handling request", "error handling request"}, logs.messages(slog.LevelError))
}

type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) messages(level slog.Level) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	var msgs []string
	for _, r := range h.records {
		if r.Level == level {
			msgs = append(msgs, r.Message)
		}
	}
	return msgs
}
