package ctasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/realtime"
	"github.com/nyaruka/mailroom/core/tasks/realtime/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestTicketClosed(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// add a ticket closed trigger
	testdb.InsertTicketClosedTrigger(rt, testdb.Org1, testdb.Favorites)
	models.FlushCache()

	testdb.InsertClosedTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, nil)
	evt := events.NewTicketClosed("01992f54-5ab6-717a-a39e-e8ca91fb7262")

	err := realtime.QueueTask(ctx, rt, testdb.Org1.ID, testdb.Ann.ID, ctasks.NewTicketClosed(evt))
	require.NoError(t, err)

	task, err := rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)

	err = tasks.Perform(ctx, rt, task)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND text = 'What is your favorite color?'`, testdb.Ann.ID).Returns(1)
}
