package web_test

import (
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

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

	for _, addr := range []string{"localhost:8190", "localhost:8191"} {
		require.Eventually(t, func() bool {
			c, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
			if err != nil {
				return false
			}
			c.Close()
			return true
		}, 5*time.Second, 10*time.Millisecond, "listener at %s never came up", addr)
	}

	const internetURL = "http://localhost:8190"
	const internalURL = "http://localhost:8191"

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
