package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestEmailSent(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	rt.Config.SMTPServer = `smtp://foo:bar@example.com?from=foo%40example.com`

	runTests(t, rt, "testdata/email_sent.json")

	// sends using the shared configuration are tallied by recipient
	assertvk.HGetAll(t, vc, "tallies:1", map[string]string{"emails/shared@2025-05-04": "3"})
	assertvk.SMembers(t, vc, "tallies:pending", []string{"1"})

	// but not once the workspace has its own configuration
	rt.DB.MustExec(`UPDATE orgs_org SET flow_smtp = 'smtp://zed:123@flows.com?from=foo%40flows.com' WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	runTests(t, rt, "testdata/email_sent.json")

	assertvk.HGetAll(t, vc, "tallies:1", map[string]string{"emails/shared@2025-05-04": "3"})
}
