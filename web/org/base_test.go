package org_test

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/web"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestDeindex(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer func() {
		rt.DB.MustExec(`UPDATE orgs_org SET is_active = true WHERE id = $1`, testdb.Org1.ID)
	}()

	rt.DB.MustExec(`UPDATE orgs_org SET is_active = false WHERE id = $1`, testdb.Org1.ID)

	defer testsuite.Reset(testsuite.ResetElastic | testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/deindex.json", nil)

	rc := rt.VK.Get()
	defer rc.Close()
	assertvk.SMembers(t, rc, "deindex:contacts", []string{"1"})
}

func TestMetrics(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	promToken := "2d26a50841ff48237238bbdd021150f6a33a4196"
	rt.DB.MustExec(`UPDATE orgs_org SET prometheus_token = $1 WHERE id = $2`, promToken, testdb.Org1.ID)

	wg := &sync.WaitGroup{}
	server := web.NewServer(ctx, rt, wg)
	server.Start()

	// wait for the server to start
	time.Sleep(time.Second)
	defer server.Stop()

	tcs := []struct {
		Label    string
		URL      string
		Username string
		Password string
		Response string
		Contains []string
	}{
		{
			Label:    "no auth provided",
			URL:      fmt.Sprintf("http://localhost:8091/mr/org/%s/metrics", testdb.Org1.UUID),
			Username: "",
			Password: "",
			Response: `{"error": "invalid authentication"}`,
		},
		{
			Label:    "invalid password (token)",
			URL:      fmt.Sprintf("http://localhost:8091/mr/org/%s/metrics", testdb.Org1.UUID),
			Username: "metrics",
			Password: "invalid",
			Response: `{"error": "invalid authentication"}`,
		},
		{
			Label:    "invalid username (always metrics)",
			URL:      fmt.Sprintf("http://localhost:8091/mr/org/%s/metrics", testdb.Org1.UUID),
			Username: "invalid",
			Password: promToken,
			Response: `{"error": "invalid authentication"}`,
		},
		{
			Label:    "valid token but wrong org",
			URL:      fmt.Sprintf("http://localhost:8091/mr/org/%s/metrics", testdb.Org2.UUID),
			Username: "metrics",
			Password: promToken,
			Response: `{"error": "invalid authentication"}`,
		},
		{
			Label:    "valid auth",
			URL:      fmt.Sprintf("http://localhost:8091/mr/org/%s/metrics", testdb.Org1.UUID),
			Username: "metrics",
			Password: promToken,
			Contains: []string{
				`rapidpro_group_contact_count{group_name="Active",group_uuid="b97f69f7-5edf-45c7-9fda-d37066eae91d",group_type="system",org="TextIt"} 124`,
				`rapidpro_group_contact_count{group_name="Doctors",group_uuid="c153e265-f7c9-4539-9dbc-9b358714b638",group_type="user",org="TextIt"} 121`,
				`rapidpro_channel_msg_count{channel_name="Vonage",channel_uuid="19012bfd-3ce3-4cae-9bb9-76cf92c73d49",channel_type="NX",msg_direction="out",msg_type="message",org="TextIt"} 0`,
			},
		},
	}

	for _, tc := range tcs {
		req, _ := http.NewRequest(http.MethodGet, tc.URL, nil)
		req.SetBasicAuth(tc.Username, tc.Password)
		resp, err := http.DefaultClient.Do(req)
		assert.NoError(t, err, "%s: received error", tc.Label)

		body, _ := io.ReadAll(resp.Body)

		if tc.Response != "" {
			assert.Equal(t, tc.Response, string(body), "%s: response mismatch", tc.Label)
		}
		for _, contains := range tc.Contains {
			assert.Contains(t, string(body), contains, "%s: does not contain: %s", tc.Label, contains)
		}
	}
}
