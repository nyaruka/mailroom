package ctasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	_ "github.com/nyaruka/mailroom/v26/core/runner/handlers"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestTicketClosed(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	// add a ticket closed trigger
	testdb.InsertTicketClosedTrigger(t, rt, testdb.Org1, testdb.Favorites)

	testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)
	evt := events.NewTicketClosed("01992f54-5ab6-717a-a39e-e8ca91fb7262", core.TicketOutcomeResolved)

	perform := func() {
		err := tasks.QueueContact(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, ctasks.NewTicketClosed(evt))
		require.NoError(t, err)

		task, err := rt.Queues.Realtime.Pop(ctx, vc)
		require.NoError(t, err)

		err = tasks.Perform(ctx, rt, task)
		require.NoError(t, err)

		require.NoError(t, rt.Queues.Realtime.Done(ctx, vc, task))
	}

	// a suspended workspace doesn't trigger the flow
	testdb.Org1.SetStatus(t, rt, models.OrgStatusSuspended)
	perform()

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O'`, testdb.Ann.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1`, testdb.Ann.UUID).Returns(0)

	// but a restricted one does
	testdb.Org1.SetStatus(t, rt, models.OrgStatusRestricted)
	perform()

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND text = 'What is your favorite color?'`, testdb.Ann.ID).Returns(1)
}
