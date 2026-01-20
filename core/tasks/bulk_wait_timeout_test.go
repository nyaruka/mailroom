package tasks_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestBulkSessionTimeout(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	defer dates.SetNowFunc(time.Now)
	dates.SetNowFunc(dates.NewFixedNow(time.Date(2024, 11, 15, 13, 59, 0, 0, time.UTC)))

	testsuite.QueueBatchTask(t, rt, testdb.Org1, &tasks.BulkWaitTimeout{
		Timeouts: []*tasks.WaitTimeout{
			{
				ContactID:   testdb.Ann.ID,
				SessionUUID: "8e2786dc-e6d0-4a6a-bbc5-4ec321d60516",
				SprintUUID:  "babdfd9e-241d-4d32-be5f-d821d1ecab31",
			},
			{
				ContactID:   testdb.Bob.ID,
				SessionUUID: "b38dcb5b-9475-423d-a6bf-253b35831f4b",
				SprintUUID:  "c4d1fcc0-ca3f-4b7e-8184-804d039a3d23",
			},
		},
	})

	assert.Equal(t, map[string]int{"bulk_wait_timeout": 1}, testsuite.FlushTasks(t, rt, "batch", "throttled"))

	testsuite.AssertContactTasks(t, rt, testdb.Org1, testdb.Ann, []string{
		`{"type":"wait_timeout","task":{"session_uuid":"8e2786dc-e6d0-4a6a-bbc5-4ec321d60516","sprint_uuid":"babdfd9e-241d-4d32-be5f-d821d1ecab31"},"queued_on":"2024-11-15T13:59:00Z"}`,
	})
	testsuite.AssertContactTasks(t, rt, testdb.Org1, testdb.Bob, []string{
		`{"type":"wait_timeout","task":{"session_uuid":"b38dcb5b-9475-423d-a6bf-253b35831f4b","sprint_uuid":"c4d1fcc0-ca3f-4b7e-8184-804d039a3d23"},"queued_on":"2024-11-15T13:59:00Z"}`,
	})
}
