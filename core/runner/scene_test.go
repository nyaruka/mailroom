package runner_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/require"
)

func TestResumeSession(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetStorage)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg)
	require.NoError(t, err)

	flow, err := oa.FlowByID(testdb.Favorites.ID)
	require.NoError(t, err)

	trigger := triggers.NewBuilder(flow.Reference()).Manual().Build()
	scenes := testsuite.StartSessions(t, rt, oa, []*testdb.Contact{testdb.Cathy}, trigger)
	sessionUUID := scenes[0].SessionUUID()

	assertdb.Query(t, rt.DB,
		`SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND current_flow_id = $2
		 AND status = 'W' AND call_id IS NULL AND output IS NOT NULL`, testdb.Cathy.ID, flow.ID()).Returns(1)

	assertdb.Query(t, rt.DB,
		`SELECT count(*) FROM flows_flowrun WHERE contact_id = $1 AND flow_id = $2
		 AND status = 'W' AND responded = FALSE AND org_id = 1`, testdb.Cathy.ID, flow.ID()).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND text like '%favorite color%'`, testdb.Cathy.ID).Returns(1)

	tcs := []struct {
		input               string
		expectedStatus      models.SessionStatus
		expectedCurrentFlow any
		expectedRunStatus   models.RunStatus
		expectedNodeUUID    any
		expectedMsgOut      string
		expectedPathLength  int
	}{
		{ // 0
			input:               "Red",
			expectedStatus:      models.SessionStatusWaiting,
			expectedCurrentFlow: int64(flow.ID()),
			expectedRunStatus:   models.RunStatusWaiting,
			expectedNodeUUID:    "48f2ecb3-8e8e-4f7b-9510-1ee08bd6a434",
			expectedMsgOut:      "Good choice, I like Red too! What is your favorite beer?",
			expectedPathLength:  4,
		},
		{ // 1
			input:               "Mutzig",
			expectedStatus:      models.SessionStatusWaiting,
			expectedCurrentFlow: int64(flow.ID()),
			expectedRunStatus:   models.RunStatusWaiting,
			expectedNodeUUID:    "a84399b1-0e7b-42ee-8759-473137b510db",
			expectedMsgOut:      "Mmmmm... delicious Mutzig. If only they made red Mutzig! Lastly, what is your name?",
			expectedPathLength:  6,
		},
		{ // 2
			input:               "Luke",
			expectedStatus:      models.SessionStatusCompleted,
			expectedCurrentFlow: nil,
			expectedRunStatus:   models.RunStatusCompleted,
			expectedNodeUUID:    nil,
			expectedMsgOut:      "Thanks Luke, we are all done!",
			expectedPathLength:  7,
		},
	}

	for i, tc := range tcs {
		testsuite.ResumeSession(t, rt, oa, testdb.Cathy, tc.input)

		assertdb.Query(t, rt.DB, `SELECT status, current_flow_id, call_id FROM flows_flowsession WHERE uuid = $1 AND output IS NOT NULL AND output_url IS NULL`, sessionUUID).
			Columns(map[string]any{
				"status": string(tc.expectedStatus), "current_flow_id": tc.expectedCurrentFlow, "call_id": nil,
			}, "%d: session mismatch", i)

		assertdb.Query(t, rt.DB, `SELECT status, responded, flow_id, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, sessionUUID).
			Columns(map[string]any{
				"status": string(tc.expectedRunStatus), "responded": true, "flow_id": int64(flow.ID()), "current_node_uuid": tc.expectedNodeUUID,
			}, "%d: run mismatch", i)

		assertdb.Query(t, rt.DB, `SELECT text FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' ORDER BY id DESC LIMIT 1`, testdb.Cathy.ID).
			Columns(map[string]any{"text": string(tc.expectedMsgOut)}, "%d: msg out mismatch", i)
	}
}
