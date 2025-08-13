package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertSessions(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	mc, _, _ := testdb.Bob.Load(rt, oa)

	sa, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdb.Bob.UUID, flows.ContactID(testdb.Bob.ID), "Bob", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	session := models.NewSession(oa, flowSession, sprint1, nil)
	err = models.InsertSessions(ctx, rt, tx, oa, []*models.Session{session}, []*models.Contact{mc})
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdb.Bob.UUID, session.ContactUUID())
	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, flow.UUID, session.CurrentFlowUUID())
	assert.NotZero(t, session.CreatedOn())
	assert.NotZero(t, session.LastSprintUUID())
	assert.Nil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_uuid::text, ended_on FROM flows_flowsession`).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_uuid": string(flow.UUID), "ended_on": nil,
		})

	flowSession, err = session.EngineSession(ctx, rt, oa.SessionAssets(), oa.Env(), flowSession.Contact(), nil)
	require.NoError(t, err)

	flowSession, sprint2, err := test.ResumeSession(flowSession, sa, "no")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	err = session.Update(ctx, rt, tx, oa, flowSession, sprint2, mc)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, flow.UUID, session.CurrentFlowUUID())

	flowSession, err = session.EngineSession(ctx, rt, oa.SessionAssets(), oa.Env(), flowSession.Contact(), nil)
	require.NoError(t, err)

	flowSession, sprint3, err := test.ResumeSession(flowSession, sa, "yes")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	err = session.Update(ctx, rt, tx, oa, flowSession, sprint3, mc)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, assets.FlowUUID(""), session.CurrentFlowUUID()) // no longer "in" a flow
	assert.NotZero(t, session.CreatedOn())
	assert.NotNil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_uuid FROM flows_flowsession`).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_uuid": nil})
}

func TestGetWaitingSessionForContact(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	sessionUUID := testdb.InsertWaitingSession(rt, testdb.Org1, testdb.Cathy, models.FlowTypeMessaging, testdb.Favorites, models.NilCallID)
	testdb.InsertFlowSession(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, models.NilCallID)
	testdb.InsertWaitingSession(rt, testdb.Org1, testdb.George, models.FlowTypeMessaging, testdb.Favorites, models.NilCallID)

	oa := testdb.Org1.Load(rt)
	mc, contact, _ := testdb.Cathy.Load(rt, oa)

	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, contact, mc.CurrentSessionUUID())
	assert.NoError(t, err)
	assert.NotNil(t, session)
	assert.Equal(t, sessionUUID, session.UUID())
}

func TestInterruptSessionsForContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, nil)
	session2UUID, run2ID := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdb.Favorites, nil)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)

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
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_uuid IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1`, run2ID).Columns(map[string]any{"status": "I"})
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForContactsTx(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, nil)
	session2UUID, run2ID := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdb.Favorites, nil)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, nil)

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
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_uuid IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1`, run2ID).Columns(map[string]any{"status": "I"})
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForChannels(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	cathy1Call := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	cathy2Call := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	bobCall := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob)
	georgeCall := testdb.InsertCall(rt, testdb.Org1, testdb.VonageChannel, testdb.George)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, cathy1Call)
	session2UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, cathy2Call)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, bobCall)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, georgeCall)

	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, cathy1Call.ID, session1UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, cathy2Call.ID, session2UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, bobCall.ID, session3UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, georgeCall.ID, session4UUID)

	err := models.InterruptSessionsForChannel(ctx, rt.DB, testdb.TwilioChannel.ID)
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // channel not included

	// check other columns are correct on interrupted session and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_uuid IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForFlows(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	cathy1Call := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	cathy2Call := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy)
	bobCall := testdb.InsertCall(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob)
	georgeCall := testdb.InsertCall(rt, testdb.Org1, testdb.VonageChannel, testdb.George)

	session1UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdb.Favorites, cathy1Call)
	session2UUID, _ := insertSessionAndRun(rt, testdb.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, cathy2Call)
	session3UUID, _ := insertSessionAndRun(rt, testdb.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.Favorites, bobCall)
	session4UUID, _ := insertSessionAndRun(rt, testdb.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdb.PickANumber, georgeCall)

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
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_uuid IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdb.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func insertSessionAndRun(rt *runtime.Runtime, contact *testdb.Contact, sessionType models.FlowType, status models.SessionStatus, flow *testdb.Flow, call *testdb.Call) (flows.SessionUUID, models.FlowRunID) {
	callID := models.NilCallID
	if call != nil {
		callID = call.ID
	}

	// create session and add a run with same status
	sessionUUID := testdb.InsertFlowSession(rt, contact, sessionType, status, flow, callID)
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
