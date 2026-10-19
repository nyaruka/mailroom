package tasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestCloseInactiveTickets(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	longAgo := time.Now().Add(-30 * 24 * time.Hour)
	recently := time.Now().Add(-1 * time.Hour)
	olderThan := time.Now().Add(-14 * 24 * time.Hour)

	// automatic closes shouldn't fire closed ticket triggers
	testdb.InsertTicketClosedTrigger(t, rt, testdb.Org1, testdb.Favorites)

	ticket1 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, longAgo, nil)
	ticket2 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.DefaultTopic, recently, nil)
	ticket3 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Bob, testdb.DefaultTopic, longAgo, nil)
	ticket4 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Cat, testdb.DefaultTopic, longAgo, nil)

	// Bob has had activity since his ticket was queued for closing
	rt.DB.MustExec(`UPDATE tickets_ticket SET last_activity_on = NOW() WHERE id = $1`, ticket3.ID)

	task := &tasks.CloseInactiveTickets{ContactIDs: []models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, OlderThan: olderThan}
	err := tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, task, false)
	assert.NoError(t, err)

	assert.Equal(t, map[string]int{"close_inactive_tickets": 1}, testsuite.FlushTasks(t, rt))

	assertdb.Query(t, rt.DB, `SELECT uuid, status FROM tickets_ticket`).Map(map[string]any{
		string(ticket1.UUID): "C",
		string(ticket2.UUID): "O",
		string(ticket3.UUID): "O",
		string(ticket4.UUID): "O",
	})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession`).Returns(0)
}
