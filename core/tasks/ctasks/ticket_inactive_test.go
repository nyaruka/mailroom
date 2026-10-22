package ctasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	_ "github.com/nyaruka/mailroom/v26/core/runner/handlers"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketInactive(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	longAgo := time.Now().Add(-40 * 24 * time.Hour).Truncate(time.Second)
	recently := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Second)

	ticket1 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, longAgo, nil)
	ticket2 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Bob, testdb.DefaultTopic, recently, nil)
	ticket3 := testdb.InsertClosedTicket(t, rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Cat, testdb.DefaultTopic, nil)
	ticket4 := testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-7658-a5d4-bdb05863ec56", testdb.Org1, testdb.Dan, testdb.DefaultTopic, longAgo, nil)

	queue := func(c *testdb.Contact, tk *testdb.Ticket) {
		require.NoError(t, tasks.QueueContact(ctx, rt, testdb.Org1.ID, c.ID, &ctasks.TicketInactive{TicketUUID: tk.UUID}))
	}

	// Ann's ticket is closed, Bob's has had activity since so gets a new fire, and Cat's is already closed
	queue(testdb.Ann, ticket1)
	queue(testdb.Bob, ticket2)
	queue(testdb.Cat, ticket3)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT uuid, status FROM tickets_ticket`).Map(map[string]any{
		string(ticket1.UUID): "C",
		string(ticket2.UUID): "O",
		string(ticket3.UUID): "C",
		string(ticket4.UUID): "O",
	})

	// closing shouldn't count as activity
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM tickets_ticket WHERE id = $1 AND last_activity_on = $2`, ticket1.ID, longAgo).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT scope FROM contacts_contactfire WHERE fire_type = 'K'`).Returns(string(ticket2.UUID))
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE fire_type = 'K' AND fire_on = $1`, recently.Add(30*24*time.Hour+time.Hour)).Returns(1)

	// if the workspace no longer closes inactive tickets, nothing happens
	rt.DB.MustExec(`UPDATE orgs_org SET max_ticket_idle_days = 0 WHERE id = $1`, testdb.Org1.ID)
	models.FlushCache()

	queue(testdb.Dan, ticket4)
	assert.Equal(t, map[string]int{"handle_contact_event": 1}, testsuite.FlushTasks(t, rt))

	assertdb.Query(t, rt.DB, `SELECT status FROM tickets_ticket WHERE id = $1`, ticket4.ID).Returns("O")
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactfire WHERE contact_id = $1`, testdb.Dan.ID).Returns(0)
}
