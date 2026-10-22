package tasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestBulkTicketInactive(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2024, 11, 15, 13, 59, 0, 0, time.UTC)))

	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.BulkTicketInactive{
		Tickets: []*tasks.InactiveTicketRef{
			{ContactID: testdb.Ann.ID, TicketUUID: "01992f54-5ab6-717a-a39e-e8ca91fb7262"},
			{ContactID: testdb.Bob.ID, TicketUUID: "01992f54-5ab6-725e-be9c-0c6407efd755"},
		},
	})

	assert.Equal(t, map[string]int{"bulk_ticket_inactive": 1}, testsuite.FlushTasks(t, rt, "batch", "throttled"))

	testsuite.AssertContactTasks(t, rt, testdb.Org1, testdb.Ann, []string{
		`{"type":"ticket_inactive","task":{"ticket_uuid":"01992f54-5ab6-717a-a39e-e8ca91fb7262"},"queued_on":"2024-11-15T13:59:00Z"}`,
	})
	testsuite.AssertContactTasks(t, rt, testdb.Org1, testdb.Bob, []string{
		`{"type":"ticket_inactive","task":{"ticket_uuid":"01992f54-5ab6-725e-be9c-0c6407efd755"},"queued_on":"2024-11-15T13:59:00Z"}`,
	})
}
