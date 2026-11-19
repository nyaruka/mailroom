package handlers_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestTicketNoteAdded(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	ticket := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC), nil)

	oa := testdb.Org1.Load(t, rt)

	subscribeTickets(t, rt)

	applyModifiers(t, rt, oa, testdb.Ann, modifiers.NewTicketNote(ticket.UUID, "call back tomorrow"))

	assert.Equal(t, []core.TicketUUID{ticket.UUID}, ticketsChangedSince(t, rt, 0))
}
