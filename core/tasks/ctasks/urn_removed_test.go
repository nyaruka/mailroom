package ctasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/urns"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestURNRemoved(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// give Ann a second URN so she has two
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, urns.URN("tel:+16055749999"), 50, nil)

	// remove the second URN
	err := tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNRemoved{URN: "tel:+16055749999"})
	require.NoError(t, err)

	task, err := rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	// URN should be detached (orphaned)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE identity = $1 AND contact_id IS NULL`, "tel:+16055749999").Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE contact_id = $1`, testdb.Ann.ID).Returns(1)

	// try to remove a URN Ann doesn't have - should be a no-op
	err = tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNRemoved{URN: "tel:+16055748888"})
	require.NoError(t, err)

	task, err = rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	// Ann still has her one URN
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE contact_id = $1`, testdb.Ann.ID).Returns(1)

	// try to remove Ann's last URN - should be refused
	err = tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNRemoved{URN: "tel:+16055741111"})
	require.NoError(t, err)

	task, err = rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	// Ann should still have her URN
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE identity = $1 AND contact_id = $2`, "tel:+16055741111", testdb.Ann.ID).Returns(1)
}
