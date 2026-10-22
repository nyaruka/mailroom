package handlers_test

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestTicketClosed(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	longAgo := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, longAgo, nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Bob, testdb.DefaultTopic, longAgo, nil)

	// contacts shouldn't have multiple open tickets but older data can
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Cat, testdb.DefaultTopic, longAgo, nil)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Cat, testdb.DefaultTopic, longAgo, nil)

	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Ann, models.ContactFireTypeTicketClose, "01992f54-5ab6-717a-a39e-e8ca91fb7262", longAgo, "")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Bob, models.ContactFireTypeTicketClose, "01992f54-5ab6-725e-be9c-0c6407efd755", longAgo, "")

	testdb.InsertTicketClosedTrigger(t, rt, testdb.Org1, testdb.BackgroundFlow)

	runTests(t, rt, "testdata/ticket_closed.json")
}
