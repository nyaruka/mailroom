package tasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestInterrupts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa := testdb.Org1.Load(t, rt)

	tcs := []struct {
		contactIDs       []models.ContactID
		flowIDs          []models.FlowID
		expectedStatuses [4]string
	}{
		{
			contactIDs:       nil,
			flowIDs:          nil,
			expectedStatuses: [4]string{"W", "W", "W", "W"},
		},
		{
			contactIDs:       []models.ContactID{testdb.Ann.ID},
			flowIDs:          nil,
			expectedStatuses: [4]string{"I", "W", "W", "W"},
		},
		{
			contactIDs:       []models.ContactID{testdb.Ann.ID, testdb.Cat.ID},
			flowIDs:          nil,
			expectedStatuses: [4]string{"I", "I", "W", "W"},
		},
		{
			contactIDs:       nil,
			flowIDs:          []models.FlowID{testdb.PickANumber.ID},
			expectedStatuses: [4]string{"W", "W", "W", "I"},
		},
		{
			contactIDs:       []models.ContactID{testdb.Ann.ID, testdb.Cat.ID},
			flowIDs:          []models.FlowID{testdb.PickANumber.ID},
			expectedStatuses: [4]string{"I", "I", "W", "I"},
		},
	}

	for i, tc := range tcs {
		// mark any remaining flow sessions as inactive
		rt.DB.MustExec(`UPDATE flows_flowsession SET status='C', ended_on=NOW() WHERE status = 'W';`)

		// twilio call
		twilioCall := testdb.InsertCall(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Dan)

		sessionUUIDs := make([]flows.SessionUUID, 4)

		// insert our dummy contact sessions
		sessionUUIDs[0] = testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Ann, models.FlowTypeMessaging, nil, testdb.Favorites)
		sessionUUIDs[1] = testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Cat, models.FlowTypeMessaging, nil, testdb.Favorites)
		sessionUUIDs[2] = testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Dan, models.FlowTypeVoice, twilioCall, testdb.Favorites)
		sessionUUIDs[3] = testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Bob, models.FlowTypeMessaging, nil, testdb.PickANumber)

		// create our task
		task := &tasks.InterruptSessions{
			ContactIDs: tc.contactIDs,
			FlowIDs:    tc.flowIDs,
		}

		// execute it
		err := task.Perform(ctx, rt, oa)
		assert.NoError(t, err)

		// check session statuses are as expected
		for j, sUUID := range sessionUUIDs {
			var status string
			err := rt.DB.Get(&status, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sUUID)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedStatuses[j], status, "%d: status mismatch for session #%d", i, j)

			// check for runs with a different status to the session
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE session_uuid = $1 AND status != $2`, sUUID, tc.expectedStatuses[j]).
				Returns(0, "%d: unexpected un-interrupted runs for session #%d", i, j)
		}
	}
}
