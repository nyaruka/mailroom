package handlers_test

import (
	"encoding/json"
	"testing"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestBroadcastCreated(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// TODO: test contacts, groups

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewSendBroadcast(handlers.NewActionUUID(), "hello world", nil, nil, nil, nil, "", []urns.URN{urns.URN("tel:+12065551212")}, nil),
				},
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   "select count(*) from flows_flowrun where contact_id = $1 AND status = 'C'",
					Args:  []any{testdb.Cathy.ID},
					Count: 1,
				},
			},
			Assertions: []handlers.Assertion{
				func(t *testing.T, rt *runtime.Runtime) error {
					vc := rt.VK.Get()
					defer vc.Close()

					task, err := rt.Queues.Batch.Pop(ctx, vc)
					assert.NoError(t, err)
					assert.NotNil(t, task)
					bcast := models.Broadcast{}
					err = json.Unmarshal(task.Task, &bcast)
					assert.NoError(t, err)
					assert.Nil(t, bcast.ContactIDs)
					assert.Nil(t, bcast.GroupIDs)
					assert.Equal(t, 1, len(bcast.URNs))
					assert.False(t, bcast.Expressions) // engine already evaluated expressions
					return nil
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)
}
