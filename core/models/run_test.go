package models_test

import (
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertAndUpdateRuns(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	sessionUUID := testdb.InsertFlowSession(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, nil, testdb.Favorites)

	t1 := time.Date(2024, 12, 3, 14, 29, 30, 0, time.UTC)
	t2 := time.Date(2024, 12, 3, 15, 13, 45, 0, time.UTC)
	t3 := time.Date(2024, 12, 3, 16, 5, 15, 0, time.UTC)

	run := &models.FlowRun{
		UUID:            "bdf93247-6629-4558-a016-433ec305757f",
		Status:          models.RunStatusWaiting,
		CreatedOn:       t1,
		ModifiedOn:      t2,
		Responded:       true,
		Results:         `{}`,
		PathNodes:       []string{"1895cae0-d3c0-4470-83df-0b4cf9449438", "3ea3c026-e1c0-4950-bb94-d4c532b1459f"},
		PathTimes:       pq.GenericArray{A: []interface{}{t1, t2}},
		CurrentNodeUUID: "5f0d8d24-0178-4b10-ae35-b3ccdc785777",
		ContactID:       testdb.Cathy.ID,
		FlowID:          testdb.Favorites.ID,
		OrgID:           testdb.Org1.ID,
		SessionUUID:     sessionUUID,
		StartID:         models.NilStartID,
	}

	tx := rt.DB.MustBegin()
	err := models.InsertRuns(ctx, tx, []*models.FlowRun{run})
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, "SELECT status, path_nodes[1]::text AS path_node1, path_nodes[2]::text AS path_node2, path_times[1]::timestamptz AS path_time1, path_times[2]::timestamptz AS path_time2 FROM flows_flowrun").Columns(map[string]any{
		"status":     "W",
		"path_node1": "1895cae0-d3c0-4470-83df-0b4cf9449438",
		"path_node2": "3ea3c026-e1c0-4950-bb94-d4c532b1459f",
		"path_time1": t1,
		"path_time2": t2,
	})

	run.Status = models.RunStatusCompleted
	run.ModifiedOn = t3
	run.ExitedOn = &t3
	run.PathNodes = []string{"1895cae0-d3c0-4470-83df-0b4cf9449438", "3ea3c026-e1c0-4950-bb94-d4c532b1459f", "5f0d8d24-0178-4b10-ae35-b3ccdc785777"}
	run.PathTimes = pq.GenericArray{A: []any{t1, t2, t3}}

	tx = rt.DB.MustBegin()
	err = models.UpdateRuns(ctx, tx, []*models.FlowRun{run})
	assert.NoError(t, err)
	assert.NoError(t, tx.Commit())

	assertdb.Query(t, rt.DB, "SELECT status, path_nodes[1]::text AS path_node1, path_nodes[2]::text AS path_node2, path_nodes[3]::text AS path_node3, path_times[1]::timestamptz AS path_time1, path_times[2]::timestamptz AS path_time2, path_times[3]::timestamptz AS path_time3 FROM flows_flowrun").Columns(map[string]any{
		"status":     "C",
		"path_node1": "1895cae0-d3c0-4470-83df-0b4cf9449438",
		"path_node2": "3ea3c026-e1c0-4950-bb94-d4c532b1459f",
		"path_node3": "5f0d8d24-0178-4b10-ae35-b3ccdc785777",
		"path_time1": t1,
		"path_time2": t2,
		"path_time3": t3,
	})
}

func TestGetContactIDsAtNode(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	createRun := func(org *testdb.Org, contact *testdb.Contact, nodeUUID flows.NodeUUID) {
		sessionUUID := testdb.InsertFlowSession(rt, contact, models.FlowTypeMessaging, models.SessionStatusWaiting, nil, testdb.Favorites)
		testdb.InsertFlowRun(rt, org, sessionUUID, contact, testdb.Favorites, models.RunStatusWaiting, nodeUUID)
	}

	createRun(testdb.Org1, testdb.Alexandra, "2fe26b10-2bb1-4115-9401-33a8a0d5d52a")
	createRun(testdb.Org1, testdb.Bob, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdb.Org1, testdb.George, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdb.Org2, testdb.Org2Contact, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2") // shouldn't be possible but..

	contactIDs, err := models.GetContactIDsAtNode(ctx, rt, testdb.Org1.ID, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []models.ContactID{testdb.Bob.ID, testdb.George.ID}, contactIDs)
}

func TestGetActiveAndWaitingRuns(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	session1UUID := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Cathy, models.FlowTypeMessaging, nil, testdb.Favorites, testdb.PickANumber)
	session2UUID := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Bob, models.FlowTypeMessaging, nil, testdb.PickANumber)
	testdb.InsertFlowSession(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusCompleted, nil, testdb.Favorites)

	runRefs, err := models.GetActiveAndWaitingRuns(ctx, rt, []flows.SessionUUID{session1UUID, session2UUID})
	assert.NoError(t, err)

	assert.Len(t, runRefs[session1UUID], 2)
	assert.Equal(t, assets.NewFlowReference(testdb.Favorites.UUID, "Favorites"), runRefs[session1UUID][0].Flow)
	assert.Equal(t, assets.NewFlowReference(testdb.PickANumber.UUID, "Pick a Number"), runRefs[session1UUID][1].Flow)
	assert.Len(t, runRefs[session2UUID], 1)
	assert.Equal(t, assets.NewFlowReference(testdb.PickANumber.UUID, "Pick a Number"), runRefs[session2UUID][0].Flow)
}

func TestInterruptRuns(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, nil)
	session2UUID, run2UUID := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdb.Favorites, nil)
	session3UUID, run3UUID := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)

	tx := rt.DB.MustBegin()

	// noop if no sessions
	err := models.InterruptRuns(ctx, tx, []flows.RunUUID{})
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted)
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting)

	tx = rt.DB.MustBegin()

	err = models.InterruptRuns(ctx, tx, []flows.RunUUID{run2UUID, run3UUID})
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // contact not included

	// check other columns are correct on interrupted session and run
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE uuid = $1`, run2UUID).Columns(map[string]any{"status": "I"})
}
