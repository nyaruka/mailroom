package runner_test

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
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionCreationAndUpdating(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(t, rt, testsuite.ResetData | testsuite.ResetValkey)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	trig := triggers.NewBuilder(flow.Reference()).Manual().Build()
	scenes := testsuite.StartSessions(t, rt, oa, []*testdb.Contact{testdb.Bob, testdb.Alexandra}, trig)
	scBob, scAlex := scenes[0], scenes[1]

	assert.Equal(t, time.Minute*5, scBob.WaitTimeout)     // Bob's messages are being sent via courier
	assert.Equal(t, time.Duration(0), scAlex.WaitTimeout) // Alexandra's messages are being sent via Android

	// check sessions and runs in database
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Bob.ID), "status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scAlex.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Alexandra.ID), "status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Bob.ID), "status": "W", "responded": false, "current_node_uuid": "cbff02b0-cd93-481d-a430-b335ab66779e",
		})
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scAlex.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Alexandra.ID), "status": "W", "responded": false, "current_node_uuid": "cbff02b0-cd93-481d-a430-b335ab66779e",
		})

	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", scBob.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 8, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", scBob.Session.UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC), // 30 days + rand(1 - 24 hours) in future
	})
	testsuite.AssertContactFires(t, rt, testdb.Alexandra.ID, map[string]time.Time{
		fmt.Sprintf("T:%s", scAlex.Session.UUID()): time.Date(2025, 2, 25, 16, 50, 27, 0, time.UTC), // 5 minutes in future
		fmt.Sprintf("E:%s", scAlex.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 21, 0, time.UTC), // 10 minutes in future
		fmt.Sprintf("S:%s", scAlex.Session.UUID()): time.Date(2025, 3, 28, 12, 9, 23, 0, time.UTC),  // 30 days + rand(1 - 24 hours) in future
	})

	scene := testsuite.ResumeSession(t, rt, oa, testdb.Bob, "no")

	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // wait doesn't have a timeout

	// check session and run in database
	assertdb.Query(t, rt.DB, `SELECT contact_id, status, session_type, current_flow_id, ended_on FROM flows_flowsession WHERE uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Bob.ID), "status": "W", "session_type": "M", "current_flow_id": int64(flow.ID), "ended_on": nil,
		})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Bob.ID), "status": "W", "responded": true, "current_node_uuid": "bd8de388-811e-4116-ab41-8c2260d5514e",
		})

	// check we have a new contact fire for wait expiration but not timeout (wait doesn't have a timeout)
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{
		fmt.Sprintf("E:%s", scBob.Session.UUID()): time.Date(2025, 2, 25, 16, 55, 43, 0, time.UTC), // updated
		fmt.Sprintf("S:%s", scBob.Session.UUID()): time.Date(2025, 3, 28, 9, 55, 36, 0, time.UTC),  // unchanged
	})

	scene = testsuite.ResumeSession(t, rt, oa, testdb.Bob, "yes")

	assert.Equal(t, flows.SessionStatusCompleted, scene.Session.Status())
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // flow has ended

	// check session and run in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession WHERE uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scBob.SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Bob.ID), "status": "C", "responded": true, "current_node_uuid": nil,
		})

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{})
}

func TestSingleSprintSession(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey | testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	flow := testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	trig := triggers.NewBuilder(flow.Reference()).Manual().Build()
	scenes := testsuite.StartSessions(t, rt, oa, []*testdb.Contact{testdb.Bob}, trig)

	// check session and run in database
	assertdb.Query(t, rt.DB, `SELECT status, session_type, current_flow_id FROM flows_flowsession WHERE uuid = $1`, scenes[0].SessionUUID()).
		Columns(map[string]any{"status": "C", "session_type": "M", "current_flow_id": nil})

	assertdb.Query(t, rt.DB, `SELECT contact_id, status, responded, current_node_uuid::text FROM flows_flowrun WHERE session_uuid = $1`, scenes[0].SessionUUID()).
		Columns(map[string]any{
			"contact_id": int64(testdb.Bob.ID), "status": "C", "responded": false, "current_node_uuid": nil,
		})

	// check we have no contact fires
	testsuite.AssertContactFires(t, rt, testdb.Bob.ID, map[string]time.Time{})
}

func TestSessionWithSubflows(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(t, rt, testsuite.ResetValkey | testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/session_test_flows.json")
	parent, child := testFlows[2], testFlows[3]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	startID := testdb.InsertFlowStart(rt, testdb.Org1, testdb.Admin, parent, []*testdb.Contact{testdb.Cathy})

	mc, contact, _ := testdb.Cathy.Load(rt, oa)
	scene := runner.NewScene(mc, contact)
	scene.Interrupt = true
	scene.StartID = startID

	err = scene.StartSession(ctx, rt, oa, triggers.NewBuilder(parent.Reference()).Manual().Build())
	require.NoError(t, err)
	err = scene.Commit(ctx, rt, oa)
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

	modelSession, err := models.GetWaitingSessionForContact(ctx, rt, oa, contact, scene.Session.UUID())
	require.NoError(t, err)
	assert.Equal(t, scene.Session.UUID(), modelSession.UUID())
	assert.Equal(t, child.ID, modelSession.CurrentFlowID())

	msg2 := flows.NewMsgIn(testdb.Cathy.URN, nil, "yes", nil, "")
	scene = runner.NewScene(mc, contact)

	err = scene.ResumeSession(ctx, rt, oa, modelSession, resumes.NewMsg(events.NewMsgReceived(msg2)))
	require.NoError(t, err)
	err = scene.Commit(ctx, rt, oa)
	require.NoError(t, err)

	assert.Equal(t, flows.SessionStatusCompleted, scene.Session.Status())
	assert.Equal(t, time.Duration(0), scene.WaitTimeout) // flow has ended

	// check we have no contact fires for wait expiration or timeout
	testsuite.AssertContactFires(t, rt, testdb.Cathy.ID, map[string]time.Time{})
}

func TestSessionFailedStart(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	dates.SetNowFunc(dates.NewSequentialNow(time.Date(2025, 2, 25, 16, 45, 0, 0, time.UTC), time.Second))
	random.SetGenerator(random.NewSeededGenerator(123))

	defer dates.SetNowFunc(time.Now)
	defer random.SetGenerator(random.DefaultGenerator)
	defer testsuite.Reset(t, rt, testsuite.ResetValkey | testsuite.ResetData)

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/ping_pong.json")
	ping, pong := testFlows[0], testFlows[1]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	trig := triggers.NewBuilder(ping.Reference()).Manual().Build()
	scenes := testsuite.StartSessions(t, rt, oa, []*testdb.Contact{testdb.Cathy}, trig)

	assert.Equal(t, flows.SessionStatusFailed, scenes[0].Session.Status())
	assert.Len(t, scenes[0].Session.Runs(), 201)

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

func TestFlowStats(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetValkey | testsuite.ResetData)

	defer random.SetGenerator(random.DefaultGenerator)
	random.SetGenerator(random.NewSeededGenerator(123))

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/flow_stats_test.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	trig := triggers.NewBuilder(flow.Reference()).Manual().Build()
	testsuite.StartSessions(t, rt, oa, []*testdb.Contact{testdb.Bob, testdb.Alexandra, testdb.George}, trig)

	// should have a single record of all 3 contacts going through the first segment
	var activityCounts []*models.FlowActivityCount
	err = rt.DB.SelectContext(ctx, &activityCounts, "SELECT flow_id, scope, count FROM flows_flowactivitycount ORDER BY flow_id, scope")
	require.NoError(t, err)
	assert.Len(t, activityCounts, 3)
	assert.Equal(t, &models.FlowActivityCount{FlowID: flow.ID, Scope: "node:072b95b3-61c3-4e0e-8dd1-eb7481083f94", Count: 3}, activityCounts[0])
	assert.Equal(t, &models.FlowActivityCount{FlowID: flow.ID, Scope: "segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", Count: 3}, activityCounts[1])
	assert.Equal(t, &models.FlowActivityCount{FlowID: flow.ID, Scope: "status:W", Count: 3}, activityCounts[2])

	// should have no result counts yet
	assertdb.Query(t, rt.DB, "SELECT count(*) FROM flows_flowresultcount").Returns(0)

	assertFlowActivityCounts(t, rt, flow.ID, map[string]int{
		"node:072b95b3-61c3-4e0e-8dd1-eb7481083f94":                                         3,
		"segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 3,
		"status:W": 3,
	})
	assertFlowResultCounts(t, rt, flow.ID, map[string]int{})

	assertvk.Keys(t, vc, "recent_contacts:*", []string{
		"recent_contacts:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", // "what's your fav color" -> color split
	})

	// all 3 contacts went from first msg to the color split - no operands recorded for this segment
	assertvk.ZRange(t, vc, "recent_contacts:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", 0, -1,
		[]string{"bzXDPJHreu|10001|", "PYVP90uqWA|10003|", "RtWDACk2SS|10002|"},
	)

	testsuite.ResumeSession(t, rt, oa, testdb.Bob, "blue")
	testsuite.ResumeSession(t, rt, oa, testdb.Alexandra, "BLUE")
	testsuite.ResumeSession(t, rt, oa, testdb.George, "teal")

	assertFlowActivityCounts(t, rt, flow.ID, map[string]int{
		"node:072b95b3-61c3-4e0e-8dd1-eb7481083f94":                                         1,
		"segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 3, // "what's your fav color" -> color split
		"segment:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e": 2, // color split :: Blue exit -> next node
		"segment:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95": 1, // color split :: Other exit -> next node
		"segment:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0": 2, // split by expression :: Other exit -> next node
		"segment:0a4f2ea9-c47f-4e9c-a242-89ae5b38d679:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 1, // "sorry I don't know that color" -> color split
		"segment:97cd44ce-dec2-4e19-8ca2-4e20db51dc08:0e1fe072-6f03-4f29-98aa-7bedbe930dab": 2, // "X is a great color" -> split by expression
		"segment:614e7451-e0bd-43d9-b317-2aded3c8d790:a1e649db-91e0-47c4-ab14-eba0d1475116": 2, // "you have X tickets" -> group split
		"status:C": 2,
		"status:W": 1,
	})
	assertFlowResultCounts(t, rt, flow.ID, map[string]int{"color/Blue": 2, "color/Other": 1})

	testsuite.ResumeSession(t, rt, oa, testdb.George, "azure")

	assertFlowActivityCounts(t, rt, flow.ID, map[string]int{
		"node:072b95b3-61c3-4e0e-8dd1-eb7481083f94":                                         1,
		"segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 3, // "what's your fav color" -> color split
		"segment:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e": 2, // color split :: Blue exit -> next node
		"segment:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95": 2, // color split :: Other exit -> next node
		"segment:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0": 2, // split by expression :: Other exit -> next node
		"segment:0a4f2ea9-c47f-4e9c-a242-89ae5b38d679:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 2, // "sorry I don't know that color" -> color split
		"segment:97cd44ce-dec2-4e19-8ca2-4e20db51dc08:0e1fe072-6f03-4f29-98aa-7bedbe930dab": 2, // "X is a great color" -> split by expression
		"segment:614e7451-e0bd-43d9-b317-2aded3c8d790:a1e649db-91e0-47c4-ab14-eba0d1475116": 2, // "you have X tickets" -> group split
		"status:C": 2,
		"status:W": 1,
	})
	assertFlowResultCounts(t, rt, flow.ID, map[string]int{"color/Blue": 2, "color/Other": 1})

	assertvk.Keys(t, vc, "recent_contacts:*", []string{
		"recent_contacts:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", // "what's your fav color" -> color split
		"recent_contacts:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e", // color split :: Blue exit -> next node
		"recent_contacts:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95", // color split :: Other exit -> next node
		"recent_contacts:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0", // split by expression :: Other exit -> next node
		"recent_contacts:0a4f2ea9-c47f-4e9c-a242-89ae5b38d679:072b95b3-61c3-4e0e-8dd1-eb7481083f94", // "sorry I don't know that color" -> color split
		"recent_contacts:97cd44ce-dec2-4e19-8ca2-4e20db51dc08:0e1fe072-6f03-4f29-98aa-7bedbe930dab", // "X is a great color" -> split by expression
		"recent_contacts:614e7451-e0bd-43d9-b317-2aded3c8d790:a1e649db-91e0-47c4-ab14-eba0d1475116", // "you have X tickets" -> group split
	})

	// check recent operands for color split :: Blue exit -> next node
	assertvk.ZRange(t, vc, "recent_contacts:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e", 0, -1,
		[]string{"5dyuJzp6MB|10001|blue", "ZZ/N3THKKL|10003|BLUE"},
	)

	// check recent operands for color split :: Other exit -> next node
	assertvk.ZRange(t, vc, "recent_contacts:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95", 0, -1,
		[]string{"bPiuaeAX6V|10002|teal", "/MpdX9skhq|10002|azure"},
	)

	// check recent operands for split by expression :: Other exit -> next node
	assertvk.ZRange(t, vc, "recent_contacts:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0", 0, -1,
		[]string{"QFoOgV99Av|10001|0", "nkcW6vAYAn|10003|0"},
	)

	testsuite.ResumeSession(t, rt, oa, testdb.George, "blue")

	assertFlowResultCounts(t, rt, flow.ID, map[string]int{"color/Blue": 3, "color/Other": 0})
}

func TestResumeSession(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData | testsuite.ResetStorage)

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

func assertFlowActivityCounts(t *testing.T, rt *runtime.Runtime, flowID models.FlowID, expected map[string]int) {
	var counts []*models.FlowActivityCount
	err := rt.DB.Select(&counts, "SELECT flow_id, scope, SUM(count) AS count FROM flows_flowactivitycount WHERE flow_id = $1 GROUP BY flow_id, scope", flowID)
	require.NoError(t, err)

	actual := make(map[string]int)
	for _, c := range counts {
		actual[c.Scope] = c.Count
	}

	assert.Equal(t, expected, actual)
}

func assertFlowResultCounts(t *testing.T, rt *runtime.Runtime, flowID models.FlowID, expected map[string]int) {
	var counts []*models.FlowResultCount
	err := rt.DB.Select(&counts, "SELECT flow_id, result, category, SUM(count) AS count FROM flows_flowresultcount WHERE flow_id = $1 GROUP BY flow_id, result, category", flowID)
	require.NoError(t, err)

	actual := make(map[string]int)
	for _, c := range counts {
		actual[fmt.Sprintf("%s/%s", c.Result, c.Category)] = c.Count
	}

	assert.Equal(t, expected, actual)
}
