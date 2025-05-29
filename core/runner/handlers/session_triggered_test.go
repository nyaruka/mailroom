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
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestSessionTriggered(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	groupRef := &assets.GroupReference{
		UUID: testdata.TestersGroup.UUID,
	}

	test.MockUniverse()

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdata.Cathy: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), testdata.SingleMessage.Reference(), []*assets.GroupReference{groupRef}, []*flows.ContactReference{testdata.George.Reference()}, "", nil, nil, true),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from flows_flowrun where contact_id = $1 AND status = 'C'",
					Args:  []any{testdata.Cathy.ID},
					Count: 1,
				},
				{ // check we don't create a start in the database
					SQL:   "select count(*) from flows_flowstart where org_id = 1",
					Count: 0,
				},
			},
			Assertions: []handlers.Assertion{
				func(t *testing.T, rt *runtime.Runtime) error {
					rc := rt.RP.Get()
					defer rc.Close()

					task, err := tasks.BatchQueue.Pop(rc)
					assert.NoError(t, err)
					assert.NotNil(t, task)
					start := models.FlowStart{}
					err = json.Unmarshal(task.Task, &start)
					assert.NoError(t, err)
					assert.True(t, start.CreateContact)
					assert.Equal(t, []models.ContactID{testdata.George.ID}, start.ContactIDs)
					assert.Equal(t, []models.GroupID{testdata.TestersGroup.ID}, start.GroupIDs)
					assert.Equal(t, testdata.SingleMessage.ID, start.FlowID)
					assert.JSONEq(t, `{"parent_uuid":"01969b47-096b-76f8-bebe-b4a1f677cf4c", "ancestors":1, "ancestors_since_input":1}`, string(start.SessionHistory))
					return nil
				},
			},
		},
		{
			Actions: handlers.ContactActionMap{
				testdata.Bob: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), testdata.IVRFlow.Reference(), nil, []*flows.ContactReference{testdata.Alexandra.Reference()}, "", nil, nil, true),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{ // check that we do have a start in the database because it's an IVR flow
					SQL:   "select count(*) from flows_flowstart where org_id = 1 AND flow_id = $1",
					Args:  []any{testdata.IVRFlow.ID},
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

	oa, err := models.GetOrgAssets(ctx, rt, testdata.Org1.ID)
	assert.NoError(t, err)

	favoriteFlow, err := oa.FlowByID(testdata.Favorites.ID)
	assert.NoError(t, err)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdata.Cathy: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), favoriteFlow.Reference(), nil, nil, "name ~ @contact.name", nil, nil, true),
				},
			},
			Assertions: []handlers.Assertion{
				func(t *testing.T, rt *runtime.Runtime) error {
					rc := rt.RP.Get()
					defer rc.Close()

					task, err := tasks.BatchQueue.Pop(rc)
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
