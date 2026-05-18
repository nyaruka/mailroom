package web_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// blank-imported so TestListeners has a registered internal route to probe
	_ "github.com/nyaruka/mailroom/v26/web/contact"
)

func TestServer(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/server.json")
}

// TestListeners verifies that public and internal endpoints are correctly split
// between the two listener ports during the dual-exposure phase.
func TestListeners(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()
	defer server.Stop()

	time.Sleep(100 * time.Millisecond)

	const publicURL = "http://localhost:8091"
	const internalURL = "http://localhost:8092"

	tcs := []struct {
		label  string
		method string
		url    string
		status int
	}{
		// public listener: index, public routes, and (during transition) internal routes
		{"public: index", "GET", publicURL + "/", 200},
		{"public: internal route via GET (wrong method)", "GET", publicURL + "/mi/contact/parse_query", 405},
		{"public: unknown path", "GET", publicURL + "/nope", 404},

		// internal listener: only /mi/* routes, no index, no /mr/*
		{"internal: index", "GET", internalURL + "/", 404},
		{"internal: internal route via GET (wrong method)", "GET", internalURL + "/mi/contact/parse_query", 405},
		{"internal: public route not exposed", "GET", internalURL + "/mr/docs", 404},
		{"internal: unknown path", "GET", internalURL + "/nope", 404},
	}

	for _, tc := range tcs {
		req, err := http.NewRequest(tc.method, tc.url, nil)
		require.NoError(t, err, tc.label)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err, tc.label)
		resp.Body.Close()

		assert.Equal(t, tc.status, resp.StatusCode, tc.label)
	}
}
