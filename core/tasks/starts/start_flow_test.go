package starts_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/starts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/assert"
)

func TestStartFlowTask(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	rc := rt.VK.Get()
	defer rc.Close()

	testdb.InsertWaitingSession(rt, testdb.Org1, testdb.George, models.FlowTypeMessaging, testdb.Favorites, models.NilCallID)

	tcs := []struct {
		flowID                   models.FlowID
		groupIDs                 []models.GroupID
		excludeGroupIDs          []models.GroupID
		contactIDs               []models.ContactID
		createContact            bool
		query                    string
		excludeInAFlow           bool
		excludeStartedPreviously bool
		queue                    queues.Fair
		expectedContactCount     int
		expectedBatchCount       int
		expectedTotalCount       int
		expectedStatus           models.StartStatus
		expectedActiveRuns       map[models.FlowID]int
	}{
		{ // 0: empty flow start
			flowID:                   testdb.Favorites.ID,
			excludeInAFlow:           true,
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Batch,
			expectedContactCount:     0,
			expectedBatchCount:       0,
			expectedTotalCount:       0,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 1, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 1: single group
			flowID:                   testdb.Favorites.ID,
			groupIDs:                 []models.GroupID{testdb.DoctorsGroup.ID},
			excludeInAFlow:           true,
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Batch,
			expectedContactCount:     121,
			expectedBatchCount:       5,
			expectedTotalCount:       121,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 122, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 2: group and contact (but all already active)
			flowID:                   testdb.Favorites.ID,
			groupIDs:                 []models.GroupID{testdb.DoctorsGroup.ID},
			contactIDs:               []models.ContactID{testdb.Cathy.ID},
			excludeInAFlow:           true,
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Batch,
			expectedContactCount:     121,
			expectedBatchCount:       0,
			expectedTotalCount:       0,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 122, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 3: don't exclude started previously
			flowID:                   testdb.Favorites.ID,
			contactIDs:               []models.ContactID{testdb.Cathy.ID},
			excludeInAFlow:           false,
			excludeStartedPreviously: false,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 122, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 4: previous group and one new contact
			flowID:                   testdb.Favorites.ID,
			groupIDs:                 []models.GroupID{testdb.DoctorsGroup.ID},
			contactIDs:               []models.ContactID{testdb.Bob.ID},
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Batch,
			expectedContactCount:     122,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 5: single contact, no restart
			flowID:                   testdb.Favorites.ID,
			contactIDs:               []models.ContactID{testdb.Bob.ID},
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       0,
			expectedTotalCount:       0,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 6: single contact, include active, but no restart
			flowID:                   testdb.Favorites.ID,
			contactIDs:               []models.ContactID{testdb.Bob.ID},
			excludeInAFlow:           false,
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       0,
			expectedTotalCount:       0,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 7: single contact, include active and restart
			flowID:                   testdb.Favorites.ID,
			contactIDs:               []models.ContactID{testdb.Bob.ID},
			excludeInAFlow:           false,
			excludeStartedPreviously: false,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 8: query start
			flowID:                   testdb.Favorites.ID,
			query:                    "bob",
			excludeInAFlow:           false,
			excludeStartedPreviously: false,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 9: query start with invalid query
			flowID:                   testdb.Favorites.ID,
			query:                    "xyz = 45",
			excludeInAFlow:           false,
			excludeStartedPreviously: false,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     0,
			expectedBatchCount:       0,
			expectedTotalCount:       0,
			expectedStatus:           models.StartStatusFailed,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 10: new contact
			flowID:               testdb.Favorites.ID,
			createContact:        true,
			queue:                rt.Queues.Handler,
			expectedContactCount: 1,
			expectedBatchCount:   1,
			expectedTotalCount:   1,
			expectedStatus:       models.StartStatusCompleted,
			expectedActiveRuns:   map[models.FlowID]int{testdb.Favorites.ID: 124, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
		{ // 11: other messaging flow
			flowID:                   testdb.PickANumber.ID,
			contactIDs:               []models.ContactID{testdb.Bob.ID},
			excludeInAFlow:           false,
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 1, testdb.BackgroundFlow.ID: 0},
		},
		{ // 12: background flow
			flowID:                   testdb.BackgroundFlow.ID,
			contactIDs:               []models.ContactID{testdb.Bob.ID},
			excludeInAFlow:           false,
			excludeStartedPreviously: true,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 123, testdb.PickANumber.ID: 1, testdb.BackgroundFlow.ID: 0},
		},
		{ // 13: exclude group
			flowID:                   testdb.Favorites.ID,
			contactIDs:               []models.ContactID{testdb.Cathy.ID, testdb.Bob.ID},
			excludeGroupIDs:          []models.GroupID{testdb.DoctorsGroup.ID}, // should exclude Cathy
			excludeInAFlow:           false,
			excludeStartedPreviously: false,
			queue:                    rt.Queues.Handler,
			expectedContactCount:     1,
			expectedBatchCount:       1,
			expectedTotalCount:       1,
			expectedStatus:           models.StartStatusCompleted,
			expectedActiveRuns:       map[models.FlowID]int{testdb.Favorites.ID: 124, testdb.PickANumber.ID: 0, testdb.BackgroundFlow.ID: 0},
		},
	}

	for i, tc := range tcs {
		testsuite.ReindexElastic(ctx)

		// handle our start task
		start := models.NewFlowStart(testdb.Org1.ID, models.StartTypeManual, tc.flowID).
			WithGroupIDs(tc.groupIDs).
			WithExcludeGroupIDs(tc.excludeGroupIDs).
			WithContactIDs(tc.contactIDs).
			WithQuery(tc.query).
			WithExcludeInAFlow(tc.excludeInAFlow).
			WithExcludeStartedPreviously(tc.excludeStartedPreviously).
			WithCreateContact(tc.createContact)

		err := models.InsertFlowStarts(ctx, rt.DB, []*models.FlowStart{start})
		assert.NoError(t, err, "%d: failed to insert start", i)

		err = tasks.Queue(ctx, rc, tc.queue, testdb.Org1.ID, &starts.StartFlowTask{FlowStart: start}, false)
		assert.NoError(t, err)

		taskCounts := testsuite.FlushTasks(t, rt)

		// assert our count of batches
		assert.Equal(t, tc.expectedBatchCount, taskCounts["start_flow_batch"], "%d: unexpected batch count", i)

		// assert our count of total flow runs created
		assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1 AND start_id = $2`, tc.flowID, start.ID).Returns(tc.expectedTotalCount, "%d: unexpected total run count", i)

		// assert final status
		assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowstart where status = $2 AND id = $1`, start.ID, tc.expectedStatus).Returns(1, "%d: status mismatch", i)

		// assert final contact count
		if tc.expectedStatus != models.StartStatusFailed {
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowstart where contact_count = $2 AND id = $1`, []any{start.ID, tc.expectedContactCount}, 1, "%d: contact count mismatch", i)
		}

		// assert count of active runs by flow
		for flowID, activeRuns := range tc.expectedActiveRuns {
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE status = 'W' AND flow_id = $1`, flowID).Returns(activeRuns, "%d: active runs mismatch for flow #%d", i, flowID)
		}
	}
}

func TestStartFlowTaskNonPersistedStart(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.VK.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetData)

	// create a start and start it...
	start := models.NewFlowStart(models.OrgID(1), models.StartTypeManual, testdb.SingleMessage.ID).
		WithContactIDs([]models.ContactID{testdb.Cathy.ID, testdb.Bob.ID})

	err := tasks.Queue(ctx, rc, rt.Queues.Throttled, testdb.Org1.ID, &starts.StartFlowTask{FlowStart: start}, false)
	assert.NoError(t, err)
	testsuite.FlushTasks(t, rt)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(2)
}
