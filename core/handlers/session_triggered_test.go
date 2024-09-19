package handlers_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/handlers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
)

func TestSessionTriggered(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdata.Org1.ID)
	assert.NoError(t, err)

	simpleFlow, err := oa.FlowByID(testdata.SingleMessage.ID)
	assert.NoError(t, err)

	contactRef := &flows.ContactReference{
		UUID: testdata.George.UUID,
	}

	groupRef := &assets.GroupReference{
		UUID: testdata.TestersGroup.UUID,
	}

	uuids.SetGenerator(uuids.NewSeededGenerator(1234567, time.Now))
	defer uuids.SetGenerator(uuids.DefaultGenerator)

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdata.Cathy: []flows.Action{
					actions.NewStartSession(handlers.NewActionUUID(), simpleFlow.Reference(), []*assets.GroupReference{groupRef}, []*flows.ContactReference{contactRef}, "", nil, nil, true),
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
					assert.Equal(t, simpleFlow.ID(), start.FlowID)
					assert.JSONEq(t, `{"parent_uuid":"39a9f95e-3641-4d19-95e0-ed866f27c829", "ancestors":1, "ancestors_since_input":1}`, string(start.SessionHistory))
					return nil
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
