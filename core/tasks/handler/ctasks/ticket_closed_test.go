package ctasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/handler"
	"github.com/nyaruka/mailroom/core/tasks/handler/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestTicketClosed(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.VK.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetAll)

	// add a ticket closed trigger
	testdb.InsertTicketClosedTrigger(rt, testdb.Org1, testdb.Favorites)

	ticket := testdb.InsertClosedTicket(rt, testdb.Org1, testdb.Cathy, testdb.DefaultTopic, nil)
	modelTicket := ticket.Load(rt)

	models.NewTicketClosedEvent(modelTicket, testdb.Admin.ID)

	err := handler.QueueTask(ctx, rt, testdb.Org1.ID, testdb.Cathy.ID, ctasks.NewTicketClosed(modelTicket.ID()))
	require.NoError(t, err)

	task, err := rt.Queues.Handler.Pop(ctx, rc)
	require.NoError(t, err)

	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND text = 'What is your favorite color?'`, testdb.Cathy.ID).Returns(1)
}
