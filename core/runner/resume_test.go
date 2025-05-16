package runner_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResume(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetStorage)

	// write sessions to s3 storage
	rt.Config.SessionStorage = "s3"

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshOrg)
	require.NoError(t, err)

	flow, err := oa.FlowByID(testdata.Favorites.ID)
	require.NoError(t, err)

	modelContact, flowContact, _ := testdata.Cathy.Load(rt, oa)

	trigger := triggers.NewBuilder(oa.Env(), flow.Reference(), flowContact).Manual().Build()
	sessions, err := runner.StartFlow(ctx, rt, oa, flow, []*models.Contact{modelContact}, []flows.Trigger{trigger}, true, models.NilStartID, models.NilCallID, nil)
	assert.NoError(t, err)
	assert.NotNil(t, sessions)

	assertdb.Query(t, rt.DB,
		`SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND current_flow_id = $2
		 AND status = 'W' AND call_id IS NULL AND output IS NULL`, modelContact.ID(), flow.ID()).Returns(1)

	assertdb.Query(t, rt.DB,
		`SELECT count(*) FROM flows_flowrun WHERE contact_id = $1 AND flow_id = $2
		 AND status = 'W' AND responded = FALSE AND org_id = 1`, modelContact.ID(), flow.ID()).Returns(1)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND text like '%favorite color%'`, modelContact.ID()).Returns(1)

	tcs := []struct {
		Message       string
		SessionStatus models.SessionStatus
		RunStatus     models.RunStatus
		Substring     string
		PathLength    int
	}{
		{"Red", models.SessionStatusWaiting, models.RunStatusWaiting, "%I like Red too%", 4},
		{"Mutzig", models.SessionStatusWaiting, models.RunStatusWaiting, "%they made red Mutzig%", 6},
		{"Luke", models.SessionStatusCompleted, models.RunStatusCompleted, "%Thanks Luke%", 7},
	}

	session := sessions[0]
	for i, tc := range tcs {
		// answer our first question
		msg := flows.NewMsgIn(flows.NewMsgUUID(), testdata.Cathy.URN, nil, tc.Message, nil, "")
		resume := resumes.NewMsg(oa.Env(), flowContact, msg)

		session, err = runner.ResumeFlow(ctx, rt, oa, session, modelContact, resume, nil)
		assert.NoError(t, err)
		assert.NotNil(t, session)

		assertdb.Query(t, rt.DB,
			`SELECT count(*) FROM flows_flowsession WHERE contact_id = $1
			 AND status = $2 AND call_id IS NULL AND output IS NULL AND output_url IS NOT NULL`, modelContact.ID(), tc.SessionStatus).
			Returns(1, "%d: didn't find expected session", i)

		runQuery := `SELECT count(*) FROM flows_flowrun WHERE contact_id = $1 AND flow_id = $2
		 AND status = $3 AND responded = TRUE AND org_id = 1 AND current_node_uuid IS NOT NULL
		 AND array_length(path_nodes, 1) = $4 AND session_uuid IS NOT NULL`

		assertdb.Query(t, rt.DB, runQuery, modelContact.ID(), flow.ID(), tc.RunStatus, tc.PathLength).
			Returns(1, "%d: didn't find expected run", i)

		assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE contact_id = $1 AND direction = 'O' AND text like $2`, modelContact.ID(), tc.Substring).
			Returns(1, "%d: didn't find expected message", i)
	}
}
