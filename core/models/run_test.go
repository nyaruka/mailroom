package models_test

import (
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestInsertAndUpdateRuns(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	sessionUUID := testdata.InsertFlowSession(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)

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
		ContactID:       testdata.Cathy.ID,
		FlowID:          testdata.Favorites.ID,
		OrgID:           testdata.Org1.ID,
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
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	createRun := func(org *testdata.Org, contact *testdata.Contact, nodeUUID flows.NodeUUID) {
		sessionUUID := testdata.InsertFlowSession(rt, contact, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)
		testdata.InsertFlowRun(rt, org, sessionUUID, contact, testdata.Favorites, models.RunStatusWaiting, nodeUUID)
	}

	createRun(testdata.Org1, testdata.Alexandra, "2fe26b10-2bb1-4115-9401-33a8a0d5d52a")
	createRun(testdata.Org1, testdata.Bob, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdata.Org1, testdata.George, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	createRun(testdata.Org2, testdata.Org2Contact, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2") // shouldn't be possible but..

	contactIDs, err := models.GetContactIDsAtNode(ctx, rt, testdata.Org1.ID, "dd79811e-a88a-4e67-bb47-a132fe8ce3f2")
	assert.NoError(t, err)
	assert.ElementsMatch(t, []models.ContactID{testdata.Bob.ID, testdata.George.ID}, contactIDs)
}
