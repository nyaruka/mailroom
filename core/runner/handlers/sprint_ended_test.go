package handlers_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/flows/resumes"
	"github.com/nyaruka/goflow/flows/triggers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionCreationAndUpdating(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mcBob, fcBob, _ := testdb.Bob.Load(rt, oa)
	mcAlex, fcAlex, _ := testdb.Alexandra.Load(rt, oa)
	trigs := []flows.Trigger{
		triggers.NewBuilder(flow.Reference()).Manual().Build(),
		triggers.NewBuilder(flow.Reference()).Manual().Build(),
	}

	scenes := []*runner.Scene{
		runner.NewScene(mcBob, fcBob, models.NilUserID),
		runner.NewScene(mcAlex, fcAlex, models.NilUserID),
	}

	err = runner.StartSessions(ctx, rt, oa, scenes, nil, trigs, true, models.NilStartID)
	require.NoError(t, err)
	assert.Equal(t, time.Minute*5, scenes[0].WaitTimeout)    // Bob's messages are being sent via courier
	assert.Equal(t, time.Duration(0), scenes[1].WaitTimeout) // Alexandra's messages are being sent via Android

	// check sessions in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE contact_id = $1`, testdb.Bob.ID).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE contact_id = $1`, testdb.Alexandra.ID).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	bobSession, alexSession := scenes[0].Session, scenes[1].Session

	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", bobSession.UUID()): time.Date(2025, 2, 25, 16, 55, 9, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", bobSession.UUID()): time.Date(2025, 3, 28, 9, 55, 37, 0, time.UTC), // 30 days + rand(1 - 24 hours) in future
	})
	testsuite.AssertContactFires(t, rt, testdb.Alexandra.ID, map[string]time.Time{
		fmt.Sprintf("T:%s", alexSession.UUID()): time.Date(2025, 2, 25, 16, 50, 28, 0, time.UTC), // 5 minutes in future
		fmt.Sprintf("E:%s", alexSession.UUID()): time.Date(2025, 2, 25, 16, 55, 22, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", alexSession.UUID()): time.Date(2025, 3, 28, 12, 9, 24, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	modelSession, err := models.GetWaitingSessionForContact(ctx, rt, oa, fcBob, bobSession.UUID())
	require.NoError(t, err)
	assert.Equal(t, bobSession.UUID(), modelSession.UUID())
	assert.Equal(t, flow.ID, modelSession.CurrentFlowID())

	msg1 := flows.NewMsgIn("0c9cd2e4-865e-40bf-92bb-3c958d5f6f0d", testdb.Bob.URN, nil, "no", nil, "")
	scene := runner.NewScene(mcBob, fcBob, models.NilUserID)

	err = runner.ResumeFlow(ctx, rt, oa, modelSession, scene, nil, resumes.NewMsg(events.NewMsgReceived(msg1)))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // wait doesn't have a timeout

	// check we have a new contact fire for wait expiration but not timeout (wait doesn't have a timeout)
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", bobSession.UUID()): time.Date(2025, 2, 25, 16, 55, 44, 0, time.UTC), // updated
		fmt.Sprintf("S:%s", bobSession.UUID()): time.Date(2025, 3, 28, 9, 55, 37, 0, time.UTC),  // unchanged
	})

	modelSession, err = models.GetWaitingSessionForContact(ctx, rt, oa, fcBob, bobSession.UUID())
	require.NoError(t, err)
	assert.Equal(t, bobSession.UUID(), modelSession.UUID())
	assert.Equal(t, flow.ID, modelSession.CurrentFlowID())

	msg2 := flows.NewMsgIn("330b1ff5-a95e-4034-b2e1-d0b0f93eb8b8", testdb.Bob.URN, nil, "yes", nil, "")
	scene = runner.NewScene(mcBob, fcBob, models.NilUserID)

	err = runner.ResumeFlow(ctx, rt, oa, modelSession, scene, nil, resumes.NewMsg(events.NewMsgReceived(msg2)))
	require.NoError(t, err)
	assert.Equal(t, flows.SessionStatusCompleted, scene.Session.Status())
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // flow has ended

	// check session in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession WHERE contact_id = $1`, testdb.Bob.ID).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{})
}

func TestSingleSprintSession(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mc, fc, _ := testdb.Bob.Load(rt, oa)
	scenes := []*runner.Scene{runner.NewScene(mc, fc, models.NilUserID)}
	trigs := []flows.Trigger{triggers.NewBuilder(flow.Reference()).Manual().Build()}

	err = runner.StartSessions(ctx, rt, oa, scenes, nil, trigs, true, models.NilStartID)
	require.NoError(t, err)

	// check session in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{})
}

func TestSessionWithSubflows(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	parent, child := testFlows[2], testFlows[3]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mc, fc, _ := testdb.Cathy.Load(rt, oa)
	scenes := []*runner.Scene{runner.NewScene(mc, fc, models.NilUserID)}
	trigs := []flows.Trigger{triggers.NewBuilder(parent.Reference()).Manual().Build()}

	err = runner.StartSessions(ctx, rt, oa, scenes, nil, trigs, true, models.NilStartID)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), scenes[0].WaitTimeout) // no timeout on wait

	// check session in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession`).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(child.ID), "ended_on": nil,
		})

	session := scenes[0].Session

	// check we have a contact fire for wait expiration but not timeout
	testsuite.AssertContactFires(t, rt, testdb.Cathy.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", session.UUID()): time.Date(2025, 2, 25, 16, 55, 16, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", session.UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	modelSession, err := models.GetWaitingSessionForContact(ctx, rt, oa, fc, session.UUID())
	require.NoError(t, err)
	assert.Equal(t, session.UUID(), modelSession.UUID())
	assert.Equal(t, child.ID, modelSession.CurrentFlowID())

	msg2 := flows.NewMsgIn("cd476f71-34f2-42d2-ae4d-b7d1c4103bd1", testdb.Cathy.URN, nil, "yes", nil, "")
	scene := runner.NewScene(mc, fc, models.NilUserID)

	err = runner.ResumeFlow(ctx, rt, oa, modelSession, scene, nil, resumes.NewMsg(events.NewMsgReceived(msg2)))
	require.NoError(t, err)
	assert.Equal(t, flows.SessionStatusCompleted, scene.Session.Status())
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // flow has ended

	// check we have no contact fires for wait expiration or timeout
	testsuite.AssertContactFires(t, rt, testdb.Cathy.ID, map[string]time.Time{})
}
