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

func TestPopulateTask(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	oa := testdb.Org1.Load(rt)
	group := testdb.InsertContactGroup(rt, testdb.Org1, "e52fee05-2f95-4445-aef6-2fe7dac2fd56", "Women", "gender = F")
	start := dates.Now()

	task := &contacts.PopulateDynamicGroupTask{
		GroupID: group.ID,
		Query:   "gender = F",
	}
	err := task.Perform(ctx, rt, oa)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1`, group.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT contact_id FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1`, group.ID).Returns(int64(testdb.Cathy.ID))
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND modified_on > $2`, testdb.Cathy.ID, start).Returns(1)
}
