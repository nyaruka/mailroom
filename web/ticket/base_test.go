package ticket

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestTicketAssign(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Bob, testdb.DefaultTopic, nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/assign.json", nil)
}

func TestTicketAddNote(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/add_note.json", nil)
}

func TestTicketChangeTopic(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.SupportTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.SalesTopic, nil)

	testsuite.RunWebTests(t, ctx, rt, "testdata/change_topic.json", nil)
}

func TestTicketClose(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	// create 2 open tickets and 1 closed one for Cathy
	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, testdb.Editor)

	testsuite.RunWebTests(t, ctx, rt, "testdata/close.json", nil)
}

func TestTicketReopen(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	// we should be able to reopen ticket #1 because Cathy has no other tickets open
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, testdb.Admin)

	// but then we won't be able to open ticket #2
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)

	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Bob, testdb.DefaultTopic, testdb.Editor)
	testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Alexandra, testdb.DefaultTopic, testdb.Editor)

	testsuite.RunWebTests(t, ctx, rt, "testdata/reopen.json", nil)
}
