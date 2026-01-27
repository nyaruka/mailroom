package tasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestInterruptContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	s1UUID := testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Ann, models.FlowTypeMessaging, nil, testdb.Favorites)
	s2UUID := testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Bob, models.FlowTypeMessaging, nil, testdb.Favorites)
	s3UUID := testdb.InsertFlowSession(t, rt, testdb.Cat, models.FlowTypeMessaging, models.SessionStatusCompleted, nil, testdb.Favorites)
	s4UUID := testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Cat, models.FlowTypeMessaging, nil, testdb.PickANumber)
	s5UUID := testdb.InsertFlowSession(t, rt, testdb.Dan, models.FlowTypeMessaging, models.SessionStatusExpired, nil, testdb.Favorites)

	// queue and perform a task to interrupt Ann and Cat
	tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, &tasks.InterruptContacts{ContactIDs: []models.ContactID{testdb.Ann.ID, testdb.Cat.ID}}, false)
	counts := testsuite.FlushTasks(t, rt)

	assert.Equal(t, map[string]int{"interrupt_contacts": 1}, counts)

	assertdb.Query(t, rt.DB, `SELECT uuid, status FROM flows_flowsession`).Map(map[string]any{
		string(s1UUID): models.SessionStatusInterrupted,
		string(s2UUID): models.SessionStatusWaiting,
		string(s3UUID): models.SessionStatusCompleted,
		string(s4UUID): models.SessionStatusInterrupted,
		string(s5UUID): models.SessionStatusExpired,
	})
}
