package models_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
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

	testFlows := testdata.ImportFlows(rt, testdata.Org1, "testdata/session_test_flows.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	modelContact, _, _ := testdata.Bob.Load(rt, oa)

	sa, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdata.Bob.UUID, flows.ContactID(testdata.Bob.ID), "Bob", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	hookCalls := 0
	hook := func(context.Context, *sqlx.Tx, *redis.Pool, *models.OrgAssets, []*models.Session) error {
		hookCalls++
		return nil
	}

	modelSessions, timeouts, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, hook, models.NilStartID, models.NilCallID)
	require.NoError(t, err)
	assert.Equal(t, 1, hookCalls)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdata.Bob.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, flow.ID, session.CurrentFlowID())
	assert.NotZero(t, session.CreatedOn())
	assert.NotZero(t, session.LastSprintUUID())
	assert.Nil(t, session.EndedOn())
	assert.Zero(t, timeouts[0]) // not used because message doesn't have a channel

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession`).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{
		fmt.Sprintf("T:%s", modelSessions[0].UUID()): time.Date(2025, 2, 25, 16, 50, 11, 0, time.UTC), // 5 minutes in future
		fmt.Sprintf("E:%s", modelSessions[0].UUID()): time.Date(2025, 2, 25, 16, 55, 7, 0, time.UTC),  // 10 minutes in future
		fmt.Sprintf("S:%s", modelSessions[0].UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	flowSession, err = session.FlowSession(ctx, rt, oa.SessionAssets(), oa.Env())
	require.NoError(t, err)

	flowSession, sprint2, err := test.ResumeSession(flowSession, sa, "no")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	timeout, err := session.Update(ctx, rt, tx, oa, flowSession, sprint2, modelContact, hook)
	require.NoError(t, err)
	assert.Zero(t, timeout)
	assert.Equal(t, 2, hookCalls)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, flow.ID, session.CurrentFlowID())

	// check we have a new contact fire for wait expiration but not timeout (wait doesn't have a timeout)
	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", modelSessions[0].UUID()): time.Date(2025, 2, 25, 16, 55, 26, 0, time.UTC), // updated
		fmt.Sprintf("S:%s", modelSessions[0].UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC),  // unchanged
	})

	flowSession, err = session.FlowSession(ctx, rt, oa.SessionAssets(), oa.Env())
	require.NoError(t, err)

	flowSession, sprint3, err := test.ResumeSession(flowSession, sa, "yes")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	timeout, err = session.Update(ctx, rt, tx, oa, flowSession, sprint3, modelContact, hook)
	require.NoError(t, err)
	assert.Zero(t, timeout)
	assert.Equal(t, 3, hookCalls)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID()) // no longer "in" a flow
	assert.NotZero(t, session.CreatedOn())
	assert.NotNil(t, session.EndedOn())

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{})
}

func TestSingleSprintSession(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdata.ImportFlows(rt, testdata.Org1, "testdata/session_test_flows.json")
	flow := testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	modelContact, _, _ := testdata.Bob.Load(rt, oa)

	_, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact(testdata.Bob.UUID, flows.ContactID(testdata.Bob.ID), "Bob", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	hookCalls := 0
	hook := func(context.Context, *sqlx.Tx, *redis.Pool, *models.OrgAssets, []*models.Session) error {
		hookCalls++
		return nil
	}

	modelSessions, timeouts, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, hook, models.NilStartID, models.NilCallID)
	require.NoError(t, err)
	assert.Equal(t, 1, hookCalls)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdata.Bob.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID())
	assert.NotZero(t, session.CreatedOn())
	assert.NotNil(t, session.EndedOn())
	assert.Zero(t, timeouts[0])

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdata.Bob.ID, map[string]time.Time{})
}

func TestSessionWithSubflows(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdata.ImportFlows(rt, testdata.Org1, "testdata/session_test_flows.json")
	parent, child := testFlows[2], testFlows[3]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	modelContact, _, _ := testdata.Cathy.Load(rt, oa)

	sa, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(parent.UUID).
		WithContact(testdata.Cathy.UUID, flows.ContactID(testdata.Cathy.ID), "Cathy", "eng", "").MustBuild()

	startID := testdata.InsertFlowStart(rt, testdata.Org1, testdata.Admin, parent, []*testdata.Contact{testdata.Cathy})

	tx := rt.DB.MustBegin()

	hookCalls := 0
	hook := func(context.Context, *sqlx.Tx, *redis.Pool, *models.OrgAssets, []*models.Session) error {
		hookCalls++
		return nil
	}

	modelSessions, timeouts, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, hook, startID, models.NilCallID)
	require.NoError(t, err)
	assert.Equal(t, 1, hookCalls)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdata.Cathy.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusWaiting, session.Status())
	assert.Equal(t, child.ID, session.CurrentFlowID())
	assert.NotZero(t, session.CreatedOn())
	assert.Nil(t, session.EndedOn())
	assert.Zero(t, timeouts[0])

	require.Len(t, session.Runs(), 2)
	assert.Equal(t, startID, session.Runs()[0].StartID)
	assert.Equal(t, models.NilStartID, session.Runs()[1].StartID)

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id, ended_on FROM flows_flowsession`).
		Columns(map[string]any{
			"status": "W", "session_type": "M", "current_flow_id": int64(child.ID), "ended_on": nil,
		})

	// check we have a contact fire for wait expiration but not timeout
	testsuite.AssertContactFires(t, rt, testdata.Cathy.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", modelSessions[0].UUID()): time.Date(2025, 2, 25, 16, 55, 15, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", modelSessions[0].UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	flowSession, err = session.FlowSession(ctx, rt, oa.SessionAssets(), oa.Env())
	require.NoError(t, err)

	flowSession, sprint2, err := test.ResumeSession(flowSession, sa, "yes")
	require.NoError(t, err)

	tx = rt.DB.MustBegin()

	timeout, err := session.Update(ctx, rt, tx, oa, flowSession, sprint2, modelContact, hook)
	require.NoError(t, err)
	assert.Zero(t, timeout)
	assert.Equal(t, 2, hookCalls)

	require.NoError(t, tx.Commit())

	assert.Equal(t, models.SessionStatusCompleted, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID())

	// check we have no contact fires for wait expiration or timeout
	testsuite.AssertContactFires(t, rt, testdata.Cathy.ID, map[string]time.Time{})
}

func TestSessionFailedStart(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(testsuite.ResetData)

	testFlows := testdata.ImportFlows(rt, testdata.Org1, "testdata/ping_pong.json")
	ping, pong := testFlows[0], testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	modelContact, _, _ := testdata.Cathy.Load(rt, oa)

	_, flowSession, sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(ping.UUID).
		WithContact(testdata.Cathy.UUID, flows.ContactID(testdata.Cathy.ID), "Cathy", "eng", "").MustBuild()

	tx := rt.DB.MustBegin()

	hookCalls := 0
	hook := func(context.Context, *sqlx.Tx, *redis.Pool, *models.OrgAssets, []*models.Session) error {
		hookCalls++
		return nil
	}

	modelSessions, timeouts, err := models.InsertSessions(ctx, rt, tx, oa, []flows.Session{flowSession}, []flows.Sprint{sprint1}, []*models.Contact{modelContact}, hook, models.NilStartID, models.NilCallID)
	require.NoError(t, err)
	assert.Equal(t, 1, hookCalls)

	require.NoError(t, tx.Commit())

	session := modelSessions[0]

	assert.Equal(t, models.FlowTypeMessaging, session.SessionType())
	assert.Equal(t, testdata.Cathy.ID, session.ContactID())
	assert.Equal(t, models.SessionStatusFailed, session.Status())
	assert.Equal(t, models.NilFlowID, session.CurrentFlowID())
	assert.NotNil(t, session.EndedOn())
	assert.Zero(t, timeouts[0])

	// check that matches what is in the db
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession`).
		Columns(map[string]any{"status": "F", "session_type": "M", "current_flow_id": nil})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL`).Returns(1)

	// check the state of all the created runs
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun`).Returns(101)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1`, ping.ID).Returns(51)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE flow_id = $1`, pong.ID).Returns(50)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowrun WHERE status = 'F' AND exited_on IS NOT NULL`).Returns(101)

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdata.Cathy.ID, map[string]time.Time{})
}

func TestGetWaitingSessionForContact(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	sessionUUID := testdata.InsertWaitingSession(rt, testdata.Org1, testdata.Cathy, models.FlowTypeMessaging, testdata.Favorites, models.NilCallID)
	testdata.InsertFlowSession(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdata.Favorites, models.NilCallID)
	testdata.InsertWaitingSession(rt, testdata.Org1, testdata.George, models.FlowTypeMessaging, testdata.Favorites, models.NilCallID)

	oa := testdata.Org1.Load(rt)
	mc, fc, _ := testdata.Cathy.Load(rt, oa)

	session, err := models.GetWaitingSessionForContact(ctx, rt, oa, fc, mc.CurrentSessionUUID())
	assert.NoError(t, err)
	assert.NotNil(t, session)
	assert.Equal(t, sessionUUID, session.UUID())
}

func TestInterruptSessionsForContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdata.Favorites, models.NilCallID)
	session2UUID, run2ID := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)
	session3UUID, _ := insertSessionAndRun(rt, testdata.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdata.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)

	// noop if no contacts
	count, err := models.InterruptSessionsForContacts(ctx, rt.DB, []models.ContactID{})
	assert.NoError(t, err)
	assert.Equal(t, 0, count)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted)
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting)

	count, err = models.InterruptSessionsForContacts(ctx, rt.DB, []models.ContactID{testdata.Cathy.ID, testdata.Bob.ID, testdata.Alexandria.ID})
	assert.NoError(t, err)
	assert.Equal(t, 2, count)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // contact not included

	// check other columns are correct on interrupted session, run and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1`, run2ID).Columns(map[string]any{"status": "I"})
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdata.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForContactsTx(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	session1UUID, _ := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdata.Favorites, models.NilCallID)
	session2UUID, run2ID := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeVoice, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)
	session3UUID, _ := insertSessionAndRun(rt, testdata.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdata.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, models.NilCallID)

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

	err = models.InterruptSessionsForContactsTx(ctx, tx, []models.ContactID{testdata.Cathy.ID, testdata.Bob.ID})
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // contact not included

	// check other columns are correct on interrupted session, run and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowrun WHERE id = $1`, run2ID).Columns(map[string]any{"status": "I"})
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdata.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForChannels(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	cathy1CallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy)
	cathy2CallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy)
	bobCallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Bob)
	georgeCallID := testdata.InsertCall(rt, testdata.Org1, testdata.VonageChannel, testdata.George)

	session1UUID, _ := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdata.Favorites, cathy1CallID)
	session2UUID, _ := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, cathy2CallID)
	session3UUID, _ := insertSessionAndRun(rt, testdata.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, bobCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdata.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, georgeCallID)

	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, cathy1CallID, session1UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, cathy2CallID, session2UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, bobCallID, session3UUID)
	rt.DB.MustExec(`UPDATE ivr_call SET session_uuid = $2 WHERE id = $1`, georgeCallID, session4UUID)

	err := models.InterruptSessionsForChannel(ctx, rt.DB, testdata.TwilioChannel.ID)
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // channel not included

	// check other columns are correct on interrupted session and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdata.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func TestInterruptSessionsForFlows(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	cathy1CallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy)
	cathy2CallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Cathy)
	bobCallID := testdata.InsertCall(rt, testdata.Org1, testdata.TwilioChannel, testdata.Bob)
	georgeCallID := testdata.InsertCall(rt, testdata.Org1, testdata.VonageChannel, testdata.George)

	session1UUID, _ := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusCompleted, testdata.Favorites, cathy1CallID)
	session2UUID, _ := insertSessionAndRun(rt, testdata.Cathy, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, cathy2CallID)
	session3UUID, _ := insertSessionAndRun(rt, testdata.Bob, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.Favorites, bobCallID)
	session4UUID, _ := insertSessionAndRun(rt, testdata.George, models.FlowTypeMessaging, models.SessionStatusWaiting, testdata.PickANumber, georgeCallID)

	// noop if no flows
	err := models.InterruptSessionsForFlows(ctx, rt.DB, []models.FlowID{})
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted)
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusWaiting)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting)

	err = models.InterruptSessionsForFlows(ctx, rt.DB, []models.FlowID{testdata.Favorites.ID})
	require.NoError(t, err)

	assertSessionAndRunStatus(t, rt, session1UUID, models.SessionStatusCompleted) // wasn't waiting
	assertSessionAndRunStatus(t, rt, session2UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session3UUID, models.SessionStatusInterrupted)
	assertSessionAndRunStatus(t, rt, session4UUID, models.SessionStatusWaiting) // flow not included

	// check other columns are correct on interrupted session and contact
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE ended_on IS NOT NULL AND current_flow_id IS NULL AND uuid = $1`, session2UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT current_session_uuid, current_flow_id FROM contacts_contact WHERE id = $1`, testdata.Cathy.ID).Columns(map[string]any{"current_session_uuid": nil, "current_flow_id": nil})
}

func insertSessionAndRun(rt *runtime.Runtime, contact *testdata.Contact, sessionType models.FlowType, status models.SessionStatus, flow *testdata.Flow, connID models.CallID) (flows.SessionUUID, models.FlowRunID) {
	// create session and add a run with same status
	sessionUUID := testdata.InsertFlowSession(rt, contact, sessionType, status, flow, connID)
	runID := testdata.InsertFlowRun(rt, testdata.Org1, sessionUUID, contact, flow, models.RunStatus(status), "")

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
