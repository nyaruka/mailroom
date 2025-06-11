package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
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

	modelContact, _, _ := testdb.Bob.Load(rt, oa)

	sa, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdb.Bob.UUID, flows.ContactID(testdb.Bob.ID), "Bob", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	modelSessions, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, []models.CallID{models.NilCallID}, models.NilStartID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdb.Bob.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, flow.ID, session.CurrentFlowID())
	assert.NotZero(t, session.CreatedOn())
	assert.NotZero(t, session.LastSprintUUID())
	assert.Nil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession`).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	flowSession, err = session.EngineSession(ctx, rt, oa.SessionAssets(), oa.Env())
	require.NoError(t, err)

	flowSession, sprint2, err := test.ResumeSession(flowSession, sa, "no")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	err = session.Update(ctx, rt, tx, oa, flowSession, sprint2, modelContact)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, flow.ID, session.CurrentFlowID())

	flowSession, err = session.EngineSession(ctx, rt, oa.SessionAssets(), oa.Env())
	require.NoError(t, err)

	flowSession, sprint3, err := test.ResumeSession(flowSession, sa, "yes")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	err = session.Update(ctx, rt, tx, oa, flowSession, sprint3, modelContact)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID()) // no longer "in" a flow
	assert.NotZero(t, session.CreatedOn())
	assert.NotNil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})
}

func TestSingleSprintSession(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	modelContact, _, _ := testdb.Bob.Load(rt, oa)

	_, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdb.Bob.UUID, flows.ContactID(testdb.Bob.ID), "Bob", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	modelSessions, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, []models.CallID{models.NilCallID}, models.NilStartID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdb.Bob.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID())
	assert.NotZero(t, session.CreatedOn())
	assert.NotNil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})
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

	modelContact, _, _ := testdb.Cathy.Load(rt, oa)

	sa, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(parent.UUID).
		WithContact(testdb.Cathy.UUID, flows.ContactID(testdb.Cathy.ID), "Cathy", "eng", "").MustBuild()

	startID := testdb.InsertFlowStart(rt, testdb.Org1, testdb.Admin, parent, []*testdb.Contact{testdb.Cathy})

	tx := rt.DB.MustBegin()

	modelSessions, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, []models.CallID{models.NilCallID}, startID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdb.Cathy.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, child.ID, session.CurrentFlowID())
	assert.NotZero(t, session.CreatedOn())
	assert.Nil(t, session.EndedOn())

	require.Len(t, session.Runs(), 2)
	assert.Equal(t, startID, session.Runs()[0].StartID)
	assert.Equal(t, models.NilStartID, session.Runs()[1].StartID)

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession`).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(child.ID), "ended_on": nil,
		})

	flowSession, err = session.EngineSession(ctx, rt, oa.SessionAssets(), oa.Env())
	require.NoError(t, err)

	flowSession, sprint2, err := test.ResumeSession(flowSession, sa, "yes")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	err = session.Update(ctx, rt, tx, oa, flowSession, sprint2, modelContact)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID())
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

	modelContact, _, _ := testdb.Cathy.Load(rt, oa)

	_, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(ping.UUID).
		WithContact(testdb.Cathy.UUID, flows.ContactID(testdb.Cathy.ID), "Cathy", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	modelSessions, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, []models.CallID{models.NilCallID}, models.NilStartID)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdb.Cathy.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusFailed, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID())
	assert.NotNil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "F", "session_type": "M", "current_flow_id": nil})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL`).Returns(1)

	// check the state of all the created runs
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(101)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1`, ping.ID).Returns(51)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1`, pong.ID).Returns(50)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE status = 'F' AND exited_on IS NOT NULL`).Returns(101)
}

func TestGetWaitingSessionForContact(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	sessionUUID := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Cathy, models.FlowTypeMessaging, testdb.Favorites, models.NilCallID)
	testdb.InsertFlowSession(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, models.NilCallID)
	testdb.InsertWaitingSession(rt, testdb.Org1, testdb.George, models.FlowTypeMessaging, testdb.Favorites, models.NilCallID)

	oa := testdb.Org1.Load(rt)
	mc, fc, _ := testdb.Cathy.Load(rt, oa)

	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, fc, mc.CurrentSessionUUID())
	assert.NoError(t, err)
	assert.NotNil(t, session)
	assert.Equal(t, sessionUUID, session.UUID())
}

func TestInterruptSessionsForContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, models.NilCallID)
	session2UUID, run2ID := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)

	// noop if no contacts
	count, err := models.InterruptSessionsForContacts(ctx, rt.DB, []models.ContactID{})
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted)
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting)

	count, err = models.InterruptSessionsForContacts(ctx, rt.DB, []models.ContactID{testdb.Cathy.ID, testdb.Bob.ID, testdb.Alexandra.ID})
	assert.NoError(t, err)
	assert.Equal(t, 2, count)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // contact not included

	// check other columns are correct on interrupted session, run and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1`, run2ID).Columns(map[string]any{"status": "I"})
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForContactsTx(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, models.NilCallID)
	session2UUID, run2ID := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, models.NilCallID)

	tx := rt.DB.MustBegin()

	// noop if no contacts
	err := models.InterruptSessionsForContactsTx(ctx, tx, []models.ContactID{})
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted)
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting)

	tx = rt.DB.MustBegin()

	err = models.InterruptSessionsForContactsTx(ctx, tx, []models.ContactID{testdb.Cathy.ID, testdb.Bob.ID})
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // contact not included

	// check other columns are correct on interrupted session, run and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1`, run2ID).Columns(map[string]any{"status": "I"})
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForChannels(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	cathy1CallID := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	cathy2CallID := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	bobCallID := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob)
	georgeCallID := testdb.InsertCall(rt, testdb.Org1, testdb.VonageChannel, testdb.George)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, cathy1CallID)
	session2UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, cathy2CallID)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, bobCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, georgeCallID)

	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, cathy1CallID, session1UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, cathy2CallID, session2UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, bobCallID, session3UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, georgeCallID, session4UUID)

	err := models.InterruptSessionsForChannel(ctx, rt.DB, testdb.TwilioChannel.ID)
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // channel not included

	// check other columns are correct on interrupted session and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForFlows(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	cathy1CallID := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	cathy2CallID := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	bobCallID := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob)
	georgeCallID := testdb.InsertCall(rt, testdb.Org1, testdb.VonageChannel, testdb.George)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, cathy1CallID)
	session2UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, cathy2CallID)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, bobCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.PickANumber, georgeCallID)

	// noop if no flows
	err := models.InterruptSessionsForFlows(ctx, rt.DB, []models.FlowID{})
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted)
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting)

	err = models.InterruptSessionsForFlows(ctx, rt.DB, []models.FlowID{testdb.Favorites.ID})
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // flow not included

	// check other columns are correct on interrupted session and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func insertSessionAndRun(rt *runtime.Runtime, contact *testdb.Contact, sessionType models.FlowType, status models.SessionStatus, flow *testdb.Flow, connID models.CallID) (flows.SessionUUID, models.FlowRunID) {
	// create session and add a run with same status
	sessionUUID := testdb.InsertFlowSession(rt, contact, sessionType, status, flow, connID)
	runID := testdb.InsertFlowRun(rt, testdb.Org1, sessionUUID, contact, flow, models.RunStatus(status), "")

	if status == models.SessionStatusWaiting {
		// mark contact as being in that flow
		rt.DB.MustExec(`UPDATE contacts_contact SET current_session_uuid = $2, current_flow_id = $3 WHERE id = $1`, contact.ID, sessionUUID, flow.ID)
	}

	return sessionUUID, runID
}

func assertSessionAndRunStatus(t *testing.T, rt *runtime.Runtime, sessionUUID flows.SessionUUID, status models.SessionStatus) {
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowsession WHERE uuid = $1`, sessionUUID).Columns(map[string]any{"status": string(status)})
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE session_uuid = $1`, sessionUUID).Columns(map[string]any{"status": string(status)})
}
