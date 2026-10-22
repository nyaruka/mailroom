package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestTicketOpened(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	runTests(t, rt, "testdata/ticket_opened.json")
}

func TestTicketOpenedWithoutIdleLimit(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	rt.DB.MustExec(`UPDATE orgs_org SET max_ticket_idle_days = 0 WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	mc, contact, _ := testdb.Ann.Load(t, rt, oa)
	scene := runner.NewScene(mc, contact)

	topic := oa.SessionAssets().Topics().Get(testdb.DefaultTopic.UUID)
	require.NoError(t, scene.ApplyModifier(ctx, rt, oa, modifiers.NewTicketOpen(topic, nil, ""), testdb.Admin.ID, ""))
	require.NoError(t, scene.Commit(ctx, rt, oa))

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE contact_id = $1 AND status = 'O'`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE fire_type = 'K'`).Returns(0)
}
