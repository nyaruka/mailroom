package contacts_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/tasks/contacts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestPopulateQueryGroupTask(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa := testdb.Org1.Load(t, rt)
	group1 := testdb.InsertContactGroup(t, rt, testdb.Org1, "e52fee05-2f95-4445-aef6-2fe7dac2fd56", "Women", "gender = F")
	group2 := testdb.InsertContactGroup(t, rt, testdb.Org1, "8d1c25ff-d9b3-43c4-9abe-7ef3d2fc6c1a", "Invalid", "!!!", testdb.Bob)

	start := dates.Now()

	task1 := &contacts.PopulateQueryGroupTask{
		GroupID: group1.ID,
		Query:   "gender = F",
	}
	err := task1.Perform(ctx, rt, oa)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactgroup WHERE id = $1`, group1.ID).Returns("R")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1`, group1.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT contact_id FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1`, group1.ID).Returns(int64(testdb.Ann.ID))
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND modified_on > $2`, testdb.Ann.ID, start).Returns(1)

	task2 := &contacts.PopulateQueryGroupTask{
		GroupID: group2.ID,
		Query:   "!!!",
	}
	err = task2.Perform(ctx, rt, oa)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT status FROM contacts_contactgroup WHERE id = $1`, group2.ID).Returns("X")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1`, group2.ID).Returns(0) // bob removed
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND modified_on > $2`, testdb.Ann.ID, start).Returns(1)
}
