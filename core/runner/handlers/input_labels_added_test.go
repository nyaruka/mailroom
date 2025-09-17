package handlers_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestInputLabelsAdded(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	reporting := assets.NewLabelReference(assets.LabelUUID("ebc4dedc-91c4-4ed4-9dd6-daa05ea82698"), "Reporting")
	testing := assets.NewLabelReference(assets.LabelUUID("a6338cdc-7938-4437-8b05-2d5d785e3a08"), "Testing")

	msg1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "start", models.MsgStatusHandled)
	msg2 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Bob, "start", models.MsgStatusHandled)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewAddInputLabels(flows.NewActionUUID(), []*assets.LabelReference{reporting}),
					actions.NewAddInputLabels(flows.NewActionUUID(), []*assets.LabelReference{testing}),
					actions.NewAddInputLabels(flows.NewActionUUID(), []*assets.LabelReference{reporting}),
				},
				testdb.Bob.UUID: []flows.Action{},
				testdb.Cat.UUID: []flows.Action{
					actions.NewAddInputLabels(flows.NewActionUUID(), []*assets.LabelReference{testing}),
					actions.NewAddInputLabels(flows.NewActionUUID(), []*assets.LabelReference{reporting}),
				},
			},
			Msgs: ContactMsgMap{
				testdb.Ann.UUID: msg1,
				testdb.Bob.UUID: msg2,
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   "select count(*) from msgs_msg_labels WHERE msg_id = $1",
					Args:    []any{msg1.ID},
					Returns: 2,
				},
				{
					Query:   "select count(*) from msgs_msg_labels WHERE msg_id = $1",
					Args:    []any{msg2.ID},
					Returns: 0,
				},
				{
					Query:   "select count(*) from msgs_msg_labels l JOIN msgs_msg m ON l.msg_id = m.id WHERE m.contact_id = $1",
					Args:    []any{testdb.Bob.ID},
					Returns: 0,
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
