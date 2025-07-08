package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestSessionTriggered(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	groupRef := &assets.GroupReference{
		UUID: testdb.TestersGroup.UUID,
	}

	test.MockUniverse()

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), testdb.SingleMessage.Reference(), []*assets.GroupReference{groupRef}, []*flows.ContactReference{testdb.George.Reference()}, "", nil, nil, true),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from flows_flowrun where contact_id = $1 AND status = 'C'",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
				{ // check we don't create a start in the database
					SQL:   "select count(*) from flows_flowstart where org_id = 1",
					Count: 0,
				},
			},
			Assertions: []handlers.Assertion{
				func(t *testing.T, rt *runtime.Runtime) error {
					rc := rt.VK.Get()
					defer rc.Close()

					task, err := rt.Queues.Batch.Pop(ctx, rc)
					assert.NoError(t, err)
					assert.NotNil(t, task)
					start := models.FlowStart{}
					err = json.Unmarshal(task.Task, &start)
					assert.NoError(t, err)
					assert.True(t, start.CreateContact)
					assert.Equal(t, []models.ContactID{testdb.George.ID}, start.ContactIDs)
					assert.Equal(t, []models.GroupID{testdb.TestersGroup.ID}, start.GroupIDs)
					assert.Equal(t, testdb.SingleMessage.ID, start.FlowID)
					assert.JSONEq(t, `{"parent_uuid":"01969b47-096b-76f8-bebe-b4a1f677cf4c", "ancestors":1, "ancestors_since_input":1}`, string(start.SessionHistory))
					return nil
				},
			},
		},
		{
			Actions: handlers.ContactActionMap{
				testdb.Bob: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), testdb.IVRFlow.Reference(), nil, []*flows.ContactReference{testdb.Alexandra.Reference()}, "", nil, nil, true),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{ // check that we do have a start in the database because it's an IVR flow
					SQL:   "select count(*) from flows_flowstart where org_id = 1 AND flow_id = $1",
					Args:  []any{testdb.IVRFlow.ID},
					Count: 1,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}

func TestQuerySessionTriggered(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	favoriteFlow, err := oa.FlowByID(testdb.Favorites.ID)
	assert.NoError(t, err)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), favoriteFlow.Reference(), nil, nil, "name ~ @contact.name", nil, nil, true),
				},
			},
			Assertions: []handlers.Assertion{
				func(t *testing.T, rt *runtime.Runtime) error {
					rc := rt.VK.Get()
					defer rc.Close()

					task, err := rt.Queues.Batch.Pop(ctx, rc)
					assert.NoError(t, err)
					assert.NotNil(t, task)
					start := models.FlowStart{}
					err = json.Unmarshal(task.Task, &start)
					assert.NoError(t, err)
					assert.Equal(t, start.CreateContact, true)
					assert.Len(t, start.ContactIDs, 0)
					assert.Len(t, start.GroupIDs, 0)
					assert.Equal(t, `name ~ "Cathy"`, string(start.Query))
					assert.Equal(t, start.FlowID, favoriteFlow.ID())
					return nil
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
