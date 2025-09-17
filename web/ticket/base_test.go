package ticket

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestTicketAddNote(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	testsuite.RunWebTests(t, rt, "testdata/add_note.json")
}

func TestTicketChangeAssignee(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Bob, testdb.DefaultTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	testsuite.RunWebTests(t, rt, "testdata/change_assignee.json")
}

func TestTicketChangeTopic(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.SupportTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.SalesTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	testsuite.RunWebTests(t, rt, "testdata/change_topic.json")
}

func TestTicketClose(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// create 2 open tickets and 1 closed one for Ann
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), nil)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, testdb.Editor)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	testsuite.RunWebTests(t, rt, "testdata/close.json")
}

func TestTicketReopen(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	// we should be able to reopen ticket #1 because Ann has no other tickets open
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, testdb.Admin)

	// but then we won't be able to open ticket #2
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)

	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Bob, testdb.DefaultTopic, testdb.Editor)
	testdb.InsertClosedTicket(rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Dan, testdb.DefaultTopic, testdb.Editor)

	testsuite.RunWebTests(t, rt, "testdata/reopen.json")
}
