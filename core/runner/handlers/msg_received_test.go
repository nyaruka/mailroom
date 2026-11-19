package handlers_test

import (
	"testing"
	"time"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestMsgReceived(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	runTests(t, rt, "testdata/msg_received.json")
}

func TestMsgReceivedTicket(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	openedOn := time.Date(2025, 5, 4, 11, 30, 0, 0, time.UTC)
	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, openedOn, nil)
	closed := testdb.InsertOpenTicket(t, rt, "019a0f6c-5e3b-7d1a-9b2e-3c4d5e6f7a8b", testdb.Org1, testdb.Bob, testdb.DefaultTopic, openedOn, nil)
	rt.DB.MustExec(`UPDATE tickets_ticket SET status = 'C', outcome = 'R', closed_on = NOW() WHERE id = $1`, closed.ID)
	testdb.InsertOpenTicket(t, rt, "019a0f6c-5e3b-7d1a-9b2e-3c4d5e6f7a8c", testdb.Org1, testdb.Cat, testdb.DefaultTopic, openedOn, nil)
	rt.DB.MustExec(`UPDATE contacts_contact SET status = 'B' WHERE id = $1`, testdb.Cat.ID)

	// only the open ticket of the unblocked contact counts as changed
	runTests(t, rt, "testdata/msg_received_ticket.json")
}
