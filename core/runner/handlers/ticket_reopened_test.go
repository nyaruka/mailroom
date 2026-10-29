package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows/modifiers"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestTicketReopened(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	ticket := testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)

	testsuite.IndexContacts(t, rt)
	oa := disableOpenTicketsGroup(t, rt)

	assertSearch(t, rt, oa, "tickets = 1", []core.ContactUUID{})

	// contacts are loaded without their closed tickets so it has to be included explicitly
	tickets, err := models.LoadTickets(ctx, rt.DB, testdb.Org1.ID, []core.TicketUUID{ticket.UUID})
	require.NoError(t, err)

	scenes, err := runner.CreateScenes(ctx, rt, oa, []models.ContactID{testdb.Ann.ID}, map[models.ContactID][]*models.Ticket{testdb.Ann.ID: tickets})
	require.NoError(t, err)
	require.NoError(t, scenes[0].ApplyModifier(ctx, rt, oa, modifiers.NewTicketReopen(ticket.UUID), testdb.Admin.ID, ""))
	require.NoError(t, scenes[0].Commit(ctx, rt, oa))

	assertdb.Query(t, rt.DB, `SELECT status FROM tickets_ticket WHERE id = $1 AND closed_on IS NULL`, ticket.ID).Returns("O")

	assertSearch(t, rt, oa, "tickets = 1", []core.ContactUUID{testdb.Ann.UUID})
}
