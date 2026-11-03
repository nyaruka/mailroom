package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/require"
)

func TestEmailSent(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	rt.Config.SMTPServer = `smtp://foo:bar@example.com?from=foo%40example.com`

	runTests(t, rt, "testdata/email_sent.json")

	// sends using the shared configuration are tallied by recipient
	assertvk.HGetAll(t, vc, "{tallies}:1", map[string]string{"emails/shared@2025-05-04": "3"})
	assertvk.SMembers(t, vc, "{tallies}:pending", []string{"1"})

	_, err := rt.Tallies.Flush(ctx, vc, models.NewTallyStore(rt.DB), 10)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count, expires_on::text FROM orgs_tally WHERE org_id = $1 AND scope = 'emails/shared@2025-05-04'`, testdb.Org1.ID).
		Columns(map[string]any{"count": int64(3), "expires_on": "2025-08-02"})

	// but not once the workspace has its own configuration
	rt.DB.MustExec(`UPDATE orgs_org SET flow_smtp = 'smtp://zed:123@flows.com?from=foo%40flows.com' WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	runTests(t, rt, "testdata/email_sent.json")

	assertvk.NotExists(t, vc, "{tallies}:1")
}
