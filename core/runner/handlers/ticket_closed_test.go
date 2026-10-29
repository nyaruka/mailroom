package handlers_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestTicketClosed(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	longAgo := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, longAgo, nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Bob, testdb.DefaultTopic, longAgo, nil)

	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Ann, models.ContactFireTypeTicketClose, "01992f54-5ab6-717a-a39e-e8ca91fb7262", longAgo, "")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Bob, models.ContactFireTypeTicketClose, "01992f54-5ab6-725e-be9c-0c6407efd755", longAgo, "")

	testdb.InsertTicketClosedTrigger(t, rt, testdb.Org1, testdb.BackgroundFlow)

	runTests(t, rt, "testdata/ticket_closed.json")
}

func TestTicketClosedIndexesContact(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Now(), nil)

	testsuite.IndexContacts(t, rt)
	oa := disableOpenTicketsGroup(t, rt)

	assertSearch(t, rt, oa, "tickets = 1", []core.ContactUUID{testdb.Ann.UUID})

	applyModifiers(t, rt, oa, testdb.Ann, modifiers.NewTicketClose("01992f54-5ab6-717a-a39e-e8ca91fb7262"))

	assertSearch(t, rt, oa, "tickets = 1", []core.ContactUUID{})
}
