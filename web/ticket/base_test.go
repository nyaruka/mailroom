package ticket

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestTicketAssign(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Bob, testdb.DefaultTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Cathy)

	testsuite.RunWebTests(t, ctx, rt, "testdata/assign.json", testsuite.ResetDynamo)
}

func TestTicketAddNote(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Cathy)

	testsuite.RunWebTests(t, ctx, rt, "testdata/add_note.json", testsuite.ResetDynamo)
}

func TestTicketChangeTopic(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Cathy, testdb.SupportTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Cathy, testdb.SalesTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Cathy)

	testsuite.RunWebTests(t, ctx, rt, "testdata/change_topic.json", testsuite.ResetDynamo)
}

func TestTicketClose(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	// create 2 open tickets and 1 closed one for Cathy
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, time.Now(), nil)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, testdb.Editor)

	testdb.OpenTicketsGroup.Add(rt, testdb.Cathy)

	testsuite.RunWebTests(t, ctx, rt, "testdata/close.json", testsuite.ResetValkey|testsuite.ResetDynamo)
}

func TestTicketReopen(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// we should be able to reopen ticket #1 because Cathy has no other tickets open
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, testdb.Admin)

	// but then we won't be able to open ticket #2
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)

	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Bob, testdb.DefaultTopic, testdb.Editor)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Alexandra, testdb.DefaultTopic, testdb.Editor)

	testsuite.RunWebTests(t, ctx, rt, "testdata/reopen.json", testsuite.ResetDynamo)
}
