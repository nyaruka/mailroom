package handlers_test

import (
	"testing"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestMsgReceived(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	now := time.Now()

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "Hello World", nil, nil, false),
				},
				testdb.Cat.UUID: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "Hello world", nil, nil, false),
				},
			},
			Msgs: ContactMsgMap{
				testdb.Ann.UUID: testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "start", models.MsgStatusPending),
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   "SELECT COUNT(*) FROM contacts_contact WHERE id = $1 AND last_seen_on > $2",
					Args:  []any{testdb.Ann.ID, now},
					Count: 1,
				},
				{
					SQL:   "SELECT COUNT(*) FROM contacts_contact WHERE id = $1 AND last_seen_on IS NULL",
					Args:  []any{testdb.Cat.ID},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "run_ended"},
				testdb.Bob.UUID: {"run_started", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs, testsuite.ResetDynamo)
}
