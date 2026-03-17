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

func TestURNAdded(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey|testsuite.ResetOpenSearch)

	// add a new URN that doesn't exist in the database
	err := tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNAdded{URN: "tel:+16055749999"})
	require.NoError(t, err)

	task, err := rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE identity = $1 AND contact_id = $2`, "tel:+16055749999", testdb.Ann.ID).Returns(1)

	// add a URN that Ann already has - should be a no-op
	err = tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNAdded{URN: "tel:+16055741111"})
	require.NoError(t, err)

	task, err = rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE contact_id = $1`, testdb.Ann.ID).Returns(2)

	// try to add a URN that belongs to another contact - should be a no-op
	testContact := testdb.InsertContact(t, rt, testdb.Org1, "01999999-0000-0000-0000-000000000001", "Zed", "", "A")
	testdb.InsertContactURN(t, rt, testdb.Org1, testContact, urns.URN("tel:+16055740001"), 100, nil)

	err = tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNAdded{URN: "tel:+16055740001"})
	require.NoError(t, err)

	task, err = rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	// URN should still belong to the other contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE identity = $1 AND contact_id = $2`, "tel:+16055740001", testContact.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE identity = $1 AND contact_id = $2`, "tel:+16055740001", testdb.Ann.ID).Returns(0)

	// claim an orphaned URN
	testdb.InsertContactURN(t, rt, testdb.Org1, nil, urns.URN("tel:+16055740000"), 0, nil)
	err = tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, &ctasks.URNAdded{URN: "tel:+16055740000"})
	require.NoError(t, err)

	task, err = rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, task)
	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE identity = $1 AND contact_id = $2`, "tel:+16055740000", testdb.Ann.ID).Returns(1)
}
