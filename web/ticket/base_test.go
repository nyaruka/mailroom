package ticket

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestTicketAddNote(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	rt.DB.MustExec(`UPDATE tickets_ticket SET last_activity_on = '2021-01-01T00:00:00Z'`)

	testsuite.RunWebTests(t, rt, "testdata/add_note.json")
}

func TestTicketChangeAssignee(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Bob, testdb.DefaultTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	rt.DB.MustExec(`UPDATE tickets_ticket SET last_activity_on = '2021-01-01T00:00:00Z'`)

	testsuite.RunWebTests(t, rt, "testdata/change_assignee.json")
}

func TestTicketChangeTopic(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.SupportTopic, time.Now(), testdb.Agent)
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.SalesTopic, nil)

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	rt.DB.MustExec(`UPDATE tickets_ticket SET last_activity_on = '2021-01-01T00:00:00Z'`)

	testsuite.RunWebTests(t, rt, "testdata/change_topic.json")
}

func TestTicketClose(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// create 2 open tickets and 1 closed one for Ann
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), testdb.Admin)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), nil)
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Ann, testdb.DefaultTopic, testdb.Editor)

	// and close fires for the open tickets
	for _, uuid := range []string{"01992f54-5ab6-717a-a39e-e8ca91fb7262", "01992f54-5ab6-725e-be9c-0c6407efd755"} {
		testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Ann, models.ContactFireTypeTicketClose, uuid, time.Now().Add(time.Hour), "")
	}

	testdb.OpenTicketsGroup.Add(rt, testdb.Ann)

	testdb.InsertTicketClosedTrigger(t, rt, testdb.Org1, testdb.Favorites)

	testsuite.RunWebTests(t, rt, "testdata/close.json")
}

func TestTicketReopen(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// we should be able to reopen ticket #1 because Ann has no other tickets open
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, testdb.Admin)

	// but then we won't be able to open ticket #2
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)

	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Bob, testdb.DefaultTopic, testdb.Editor)
	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Dan, testdb.DefaultTopic, testdb.Editor)

	testsuite.RunWebTests(t, rt, "testdata/reopen.json")
}
