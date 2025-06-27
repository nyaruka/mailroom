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
	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mcBob, fcBob, _ := testdb.Bob.Load(rt, oa)
	mcAlex, fcAlex, _ := testdb.Alexandra.Load(rt, oa)
	scBob, scAlex := runner.NewScene(mcBob, fcBob, models.NilUserID), runner.NewScene(mcAlex, fcAlex, models.NilUserID)
	scBob.Interrupt = true
	scAlex.Interrupt = true

	trigs := []flows.Trigger{
		triggers.NewBuilder(flow.Reference()).Manual().Build(),
		triggers.NewBuilder(flow.Reference()).Manual().Build(),
	}

	err = runner.StartSessions(ctx, rt, oa, []*runner.Scene{scBob, scAlex}, trigs)
	require.NoError(t, err)
	assert.Equal(t, time.Minute*5, scBob.WaitTimeout)     // Bob's messages are being sent via courier
	assert.Equal(t, time.Duration(0), scAlex.WaitTimeout) // Alexandra's messages are being sent via Android

	// check sessions and runs in database
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcBob.ID()), "status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scAlex.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcAlex.ID()), "status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcBob.ID()), "status": "W", "responded": false, "current_node_uuid": "cbff02b0-cd93-481d-a430-b335ab66779e",
		})
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scAlex.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcAlex.ID()), "status": "W", "responded": false, "current_node_uuid": "cbff02b0-cd93-481d-a430-b335ab66779e",
		})

	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", scBob.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 9, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", scBob.Session.UUID()): time.Date(2025, 3, 28, 9, 55, 37, 0, time.UTC), // 30 days + rand(1 - 24 hours) in future
	})
	testsuite.AssertContactFires(t, rt, testdb.Alexandra.ID, map[string]time.Time{
		fmt.Sprintf("T:%s", scAlex.Session.UUID()): time.Date(2025, 2, 25, 16, 50, 28, 0, time.UTC), // 5 minutes in future
		fmt.Sprintf("E:%s", scAlex.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 22, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", scAlex.Session.UUID()): time.Date(2025, 3, 28, 12, 9, 24, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	modelSession, err := models.GetWaitingSessionForContact(ctx, rt, oa, fcBob, scBob.Session.UUID())
	require.NoError(t, err)
	assert.Equal(t, scBob.Session.UUID(), modelSession.UUID())
	assert.Equal(t, flow.ID, modelSession.CurrentFlowID())

	msg1 := flows.NewMsgIn("0c9cd2e4-865e-40bf-92bb-3c958d5f6f0d", testdb.Bob.URN, nil, "no", nil, "")
	scene := runner.NewScene(mcBob, fcBob, models.NilUserID)

	err = runner.ResumeSession(ctx, rt, oa, modelSession, scene, resumes.NewMsg(events.NewMsgReceived(msg1)))
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // wait doesn't have a timeout

	// check session and run in database
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcBob.ID()), "status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcBob.ID()), "status": "W", "responded": true, "current_node_uuid": "bd8de388-811e-4116-ab41-8c2260d5514e",
		})

	// check we have a new contact fire for wait expiration but not timeout (wait doesn't have a timeout)
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", scBob.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 44, 0, time.UTC), // updated
		fmt.Sprintf("S:%s", scBob.Session.UUID()): time.Date(2025, 3, 28, 9, 55, 37, 0, time.UTC),  // unchanged
	})

	modelSession, err = models.GetWaitingSessionForContact(ctx, rt, oa, fcBob, scBob.Session.UUID())
	require.NoError(t, err)
	assert.Equal(t, scBob.Session.UUID(), modelSession.UUID())
	assert.Equal(t, flow.ID, modelSession.CurrentFlowID())

	msg2 := flows.NewMsgIn("330b1ff5-a95e-4034-b2e1-d0b0f93eb8b8", testdb.Bob.URN, nil, "yes", nil, "")
	scene = runner.NewScene(mcBob, fcBob, models.NilUserID)

	err = runner.ResumeSession(ctx, rt, oa, modelSession, scene, resumes.NewMsg(events.NewMsgReceived(msg2)))
	require.NoError(t, err)
	assert.Equal(t, flows.SessionStatusCompleted, scene.Session.Status())
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // flow has ended

	// check session and run in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession WHERE uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mcBob.ID()), "status": "C", "responded": true, "current_node_uuid": nil,
		})

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
	scene := runner.NewScene(mc, fc, models.NilUserID)
	scene.Interrupt = true

	trigs := []flows.Trigger{triggers.NewBuilder(flow.Reference()).Manual().Build()}

	err = runner.StartSessions(ctx, rt, oa, []*runner.Scene{scene}, trigs)
	require.NoError(t, err)

	// check session and run in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession WHERE uuid = $1`, scene.SessionUUID()).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scene.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(mc.ID()), "status": "C", "responded": false, "current_node_uuid": nil,
		})

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

	startID := testdb.InsertFlowStart(rt, testdb.Org1, testdb.Admin, parent, []*testdb.Contact{testdb.Cathy})

	mc, fc, _ := testdb.Cathy.Load(rt, oa)
	scene := runner.NewScene(mc, fc, models.NilUserID)
	scene.Interrupt = true
	scene.StartID = startID

	trigs := []flows.Trigger{triggers.NewBuilder(parent.Reference()).Manual().Build()}

	err = runner.StartSessions(ctx, rt, oa, []*runner.Scene{scene}, trigs)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // no timeout on wait

	// check session amd runs in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scene.SessionUUID()).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(child.ID), "ended_on": nil,
		})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE session_uuid = $1`, scene.SessionUUID()).Returns(2)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE session_uuid = $1 AND start_id IS NOT NULL`, scene.SessionUUID()).
		Columns(map[string]any{"status": "A"})
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE session_uuid = $1 AND start_id IS NULL`, scene.SessionUUID()).
		Columns(map[string]any{"status": "W"})

	// check we have a contact fire for wait expiration but not timeout
	testsuite.AssertContactFires(t, rt, testdb.Cathy.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", scene.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 16, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", scene.Session.UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	modelSession, err := models.GetWaitingSessionForContact(ctx, rt, oa, fc, scene.Session.UUID())
	require.NoError(t, err)
	assert.Equal(t, scene.Session.UUID(), modelSession.UUID())
	assert.Equal(t, child.ID, modelSession.CurrentFlowID())

	msg2 := flows.NewMsgIn("cd476f71-34f2-42d2-ae4d-b7d1c4103bd1", testdb.Cathy.URN, nil, "yes", nil, "")
	scene = runner.NewScene(mc, fc, models.NilUserID)

	err = runner.ResumeSession(ctx, rt, oa, modelSession, scene, resumes.NewMsg(events.NewMsgReceived(msg2)))
	require.NoError(t, err)
	assert.Equal(t, flows.SessionStatusCompleted, scene.Session.Status())
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // flow has ended

	// check we have no contact fires for wait expiration or timeout
	testsuite.AssertContactFires(t, rt, testdb.Cathy.ID, map[string]time.Time{})
}

func TestSessionFailedStart(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/ping_pong.json")
	ping, pong := testFlows[0], testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mc, fc, _ := testdb.Cathy.Load(rt, oa)
	scene := runner.NewScene(mc, fc, models.NilUserID)
	scene.Interrupt = true

	trigs := []flows.Trigger{triggers.NewBuilder(ping.Reference()).Manual().Build()}

	err = runner.StartSessions(ctx, rt, oa, []*runner.Scene{scene}, trigs)
	require.NoError(t, err)
	assert.Equal(t, flows.SessionStatusFailed, scene.Session.Status())
	assert.Len(t, scene.Session.Runs(), 201)

	// check session in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "F", "session_type": "M", "current_flow_id": nil})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL`).Returns(1)

	// check the state of all the created runs
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(201)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1`, ping.ID).Returns(101)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1`, pong.ID).Returns(100)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE status = 'F' AND exited_on IS NOT NULL`).Returns(201)
}
