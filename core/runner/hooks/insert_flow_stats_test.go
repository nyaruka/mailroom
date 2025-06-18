package hooks_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/runner/hooks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInsertFlowStats(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetValkey | testsuite.ResetData)

	defer random.SetGenerator(random.DefaultGenerator)
	random.SetGenerator(random.NewSeededGenerator(123))

	testFlows := testdb.ImportFlows(rt, testdb.Org1, "testdata/flow_stats_test.json")
	flow := testFlows[0]

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshFlows)
	require.NoError(t, err)

	sa1, session1, session1Sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact("4ad4f0a6-fb95-4845-b4cb-335f67eafe96", 123, "Bob", "eng", "").MustBuild()
	sa2, session2, session2Sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact("5cfe8b70-0d4a-4862-8fb5-e72603d832a9", 234, "Ann", "eng", "").MustBuild()
	sa3, session3, session3Sprint1 := test.NewSessionBuilder().WithAssets(oa.SessionAssets()).WithFlow(flow.UUID).
		WithContact("367c8ef2-aac7-4264-9a03-40877371995d", 345, "Jim", "eng", "").MustBuild()

	tx := rt.DB.MustBeginTx(ctx, nil)

	scene1 := runner.NewScene(session1.Contact(), models.NilUserID, nil)
	scene1.AddSprint(session1, session1Sprint1, nil, false)
	scene2 := runner.NewScene(session2.Contact(), models.NilUserID, nil)
	scene2.AddSprint(session2, session2Sprint1, nil, false)
	scene3 := runner.NewScene(session3.Contact(), models.NilUserID, nil)
	scene3.AddSprint(session3, session3Sprint1, nil, false)

	err = hooks.InsertFlowStats.Execute(ctx, rt, tx, oa, map[*runner.Scene][]any{scene1: nil, scene2: nil, scene3: nil})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	// should have a single record of all 3 contacts going through the first segment
	var activityCounts []*models.FlowActivityCount
	err = rt.DB.SelectContext(ctx, &activityCounts, "SELECT flow_id, scope, count FROM flows_flowactivitycount ORDER BY flow_id, scope")
	require.NoError(t, err)
	assert.Len(t, activityCounts, 1)
	assert.Equal(t, &models.FlowActivityCount{FlowID: flow.ID, Scope: "segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", Count: 3}, activityCounts[0])

	// should have no result counts yet
	assertdb.Query(t, rt.DB, "SELECT count(*) FROM flows_flowresultcount").Returns(0)

	assertFlowActivityCounts(t, rt, flow.ID, map[string]int{"segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 3})
	assertFlowResultCounts(t, rt, flow.ID, map[string]int{})

	assertvk.Keys(t, rc, "*", []string{
		"recent_contacts:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", // "what's your fav color" -> color split
	})

	// all 3 contacts went from first msg to the color split - no operands recorded for this segment
	assertvk.ZRange(t, rc, "recent_contacts:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", 0, -1,
		[]string{"LZbbzXDPJH|123|", "reuPYVP90u|234|", "qWARtWDACk|345|"},
	)

	session1, session1Sprint2, err := test.ResumeSession(session1, sa1, "blue")
	require.NoError(t, err)
	session2, session2Sprint2, err := test.ResumeSession(session2, sa2, "BLUE")
	require.NoError(t, err)
	session3, session3Sprint2, err := test.ResumeSession(session3, sa3, "teal")
	require.NoError(t, err)

	scene4 := runner.NewScene(session1.Contact(), models.NilUserID, nil)
	scene4.AddSprint(session1, session1Sprint2, nil, false)
	scene5 := runner.NewScene(session2.Contact(), models.NilUserID, nil)
	scene5.AddSprint(session2, session2Sprint2, nil, false)
	scene6 := runner.NewScene(session3.Contact(), models.NilUserID, nil)
	scene6.AddSprint(session3, session3Sprint2, nil, false)

	tx = rt.DB.MustBeginTx(ctx, nil)

	err = hooks.InsertFlowStats.Execute(ctx, rt, tx, oa, map[*runner.Scene][]any{scene4: nil, scene5: nil, scene6: nil})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assertFlowActivityCounts(t, rt, flow.ID, map[string]int{
		"segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 3, // "what's your fav color" -> color split
		"segment:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e": 2, // color split :: Blue exit -> next node
		"segment:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95": 1, // color split :: Other exit -> next node
		"segment:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0": 2, // split by expression :: Other exit -> next node
		"segment:0a4f2ea9-c47f-4e9c-a242-89ae5b38d679:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 1, // "sorry I don't know that color" -> color split
		"segment:97cd44ce-dec2-4e19-8ca2-4e20db51dc08:0e1fe072-6f03-4f29-98aa-7bedbe930dab": 2, // "X is a great color" -> split by expression
		"segment:614e7451-e0bd-43d9-b317-2aded3c8d790:a1e649db-91e0-47c4-ab14-eba0d1475116": 2, // "you have X tickets" -> group split
	})
	assertFlowResultCounts(t, rt, flow.ID, map[string]int{"color/Blue": 2, "color/Other": 1})

	session3, session3Sprint3, err := test.ResumeSession(session3, sa3, "azure")
	require.NoError(t, err)

	scene7 := runner.NewScene(session3.Contact(), models.NilUserID, nil)
	scene7.AddSprint(session3, session3Sprint3, nil, false)

	tx = rt.DB.MustBeginTx(ctx, nil)

	err = hooks.InsertFlowStats.Execute(ctx, rt, tx, oa, map[*runner.Scene][]any{scene7: nil})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assertFlowActivityCounts(t, rt, flow.ID, map[string]int{
		"segment:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 3, // "what's your fav color" -> color split
		"segment:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e": 2, // color split :: Blue exit -> next node
		"segment:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95": 2, // color split :: Other exit -> next node
		"segment:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0": 2, // split by expression :: Other exit -> next node
		"segment:0a4f2ea9-c47f-4e9c-a242-89ae5b38d679:072b95b3-61c3-4e0e-8dd1-eb7481083f94": 2, // "sorry I don't know that color" -> color split
		"segment:97cd44ce-dec2-4e19-8ca2-4e20db51dc08:0e1fe072-6f03-4f29-98aa-7bedbe930dab": 2, // "X is a great color" -> split by expression
		"segment:614e7451-e0bd-43d9-b317-2aded3c8d790:a1e649db-91e0-47c4-ab14-eba0d1475116": 2, // "you have X tickets" -> group split
	})
	assertFlowResultCounts(t, rt, flow.ID, map[string]int{"color/Blue": 2, "color/Other": 1})

	assertvk.Keys(t, rc, "*", []string{
		"recent_contacts:5fd2e537-0534-4c12-8425-bef87af09d46:072b95b3-61c3-4e0e-8dd1-eb7481083f94", // "what's your fav color" -> color split
		"recent_contacts:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e", // color split :: Blue exit -> next node
		"recent_contacts:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95", // color split :: Other exit -> next node
		"recent_contacts:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0", // split by expression :: Other exit -> next node
		"recent_contacts:0a4f2ea9-c47f-4e9c-a242-89ae5b38d679:072b95b3-61c3-4e0e-8dd1-eb7481083f94", // "sorry I don't know that color" -> color split
		"recent_contacts:97cd44ce-dec2-4e19-8ca2-4e20db51dc08:0e1fe072-6f03-4f29-98aa-7bedbe930dab", // "X is a great color" -> split by expression
		"recent_contacts:614e7451-e0bd-43d9-b317-2aded3c8d790:a1e649db-91e0-47c4-ab14-eba0d1475116", // "you have X tickets" -> group split
	})

	// check recent operands for color split :: Blue exit -> next node
	assertvk.ZRange(t, rc, "recent_contacts:c02fc3ba-369a-4c87-9bc4-c3b376bda6d2:57b50d33-2b5a-4726-82de-9848c61eff6e", 0, -1,
		[]string{"2SS5dyuJzp|123|blue", "2MsZZ/N3TH|234|BLUE"},
	)

	// check recent operands for color split :: Other exit -> next node
	assertvk.ZRange(t, rc, "recent_contacts:ea6c38dc-11e2-4616-9f3e-577e44765d44:8712db6b-25ff-4789-892c-581f24eeeb95", 0, -1,
		[]string{"uI8bPiuaeA|345|teal", "2Vz/MpdX9s|345|azure"},
	)

	// check recent operands for split by expression :: Other exit -> next node
	assertvk.ZRange(t, rc, "recent_contacts:2b698218-87e5-4ab8-922e-e65f91d12c10:88d8bf00-51ce-4e5e-aae8-4f957a0761a0", 0, -1,
		[]string{"PLQQFoOgV9|123|0", "/cgnkcW6vA|234|0"},
	)

	// check that category counts are updated correctly when result changes
	session3, session3Sprint4, err := test.ResumeSession(session3, sa3, "blue")
	require.NoError(t, err)

	scene8 := runner.NewScene(session3.Contact(), models.NilUserID, nil)
	scene8.AddSprint(session3, session3Sprint4, nil, false)

	tx = rt.DB.MustBeginTx(ctx, nil)

	err = hooks.InsertFlowStats.Execute(ctx, rt, tx, oa, map[*runner.Scene][]any{scene8: nil})
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assertFlowResultCounts(t, rt, flow.ID, map[string]int{"color/Blue": 3, "color/Other": 0})
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
