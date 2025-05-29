package interrupts_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks/interrupts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestInterrupts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	oa := testdata.Org1.Load(rt)

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
			contactIDs:       []models.ContactID{testdata.Cathy.ID},
			flowIDs:          nil,
			expectedStatuses: [4]string{"I", "W", "W", "W"},
		},
		{
			contactIDs:       []models.ContactID{testdata.Cathy.ID, testdata.George.ID},
			flowIDs:          nil,
			expectedStatuses: [4]string{"I", "I", "W", "W"},
		},
		{
			contactIDs:       nil,
			flowIDs:          []models.FlowID{testdata.PickANumber.ID},
			expectedStatuses: [4]string{"W", "W", "W", "I"},
		},
		{
			contactIDs:       []models.ContactID{testdata.Cathy.ID, testdata.George.ID},
			flowIDs:          []models.FlowID{testdata.PickANumber.ID},
			expectedStatuses: [4]string{"I", "I", "W", "I"},
		},
	}

	for i, tc := range tcs {
		// mark any remaining flow sessions as inactive
		rt.DB.MustExec(`UPDATE flows_flowsession SET status='C', ended_on=NOW() WHERE status = 'W';`)

		// twilio call
		twilioCallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Alexandra)

		sessionUUIDs := make([]flows.SessionUUID, 4)

		// insert our dummy contact sessions
		sessionUUIDs[0] = testdata.InsertWaitingSession(rt, testdata.Org1, testdata.Cathy, models.FlowTypeMessaging, testdata.Favorites, models.NilCallID)
		sessionUUIDs[1] = testdata.InsertWaitingSession(rt, testdata.Org1, testdata.George, models.FlowTypeMessaging, testdata.Favorites, models.NilCallID)
		sessionUUIDs[2] = testdata.InsertWaitingSession(rt, testdata.Org1, testdata.Alexandra, models.FlowTypeVoice, testdata.Favorites, twilioCallID)
		sessionUUIDs[3] = testdata.InsertWaitingSession(rt, testdata.Org1, testdata.Bob, models.FlowTypeMessaging, testdata.PickANumber, models.NilCallID)

		// create our task
		task := &interrupts.InterruptSessionsTask{
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
