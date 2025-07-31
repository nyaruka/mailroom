package handlers_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestOptinRequested(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	optIn := testdb.InsertOptIn(rt, testdb.Org1, "Jokes")
	models.FlushCache()

	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:12345', scheme='facebook', path='12345' WHERE contact_id = $1`, testdb.Cathy.ID)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:23456', scheme='facebook', path='23456' WHERE contact_id = $1`, testdb.George.ID)

	msg1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "start", models.MsgStatusHandled)

	oa := testdb.Org1.Load(rt)
	ch := oa.ChannelByUUID("0f661e8b-ea9d-4bd3-9953-d368340acf91")
	assert.Equal(t, models.ChannelType("FBA"), ch.Type())
	assert.Equal(t, []assets.ChannelFeature{assets.ChannelFeatureOptIns}, ch.Features())

	tcs := []handlers.TestCase{
		{
			Actions: handlers.ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewRequestOptIn(handlers.NewActionUUID(), assets.NewOptInReference(optIn.UUID, "Jokes")),
				},
				testdb.George: []flows.Action{
					actions.NewRequestOptIn(handlers.NewActionUUID(), assets.NewOptInReference(optIn.UUID, "Jokes")),
				},
				testdb.Bob: []flows.Action{
					actions.NewRequestOptIn(handlers.NewActionUUID(), assets.NewOptInReference(optIn.UUID, "Jokes")),
				},
			},
			Msgs: handlers.ContactMsgMap{
				testdb.Cathy: msg1,
			},
			SQLAssertions: []handlers.SQLAssertion{
				{
					SQL:   `SELECT COUNT(*) FROM msgs_msg WHERE direction = 'O' AND text = '' AND high_priority = true AND contact_id = $1 AND optin_id = $2`,
					Args:  []any{testdb.Cathy.ID, optIn.ID},
					Count: 1,
				},
				{
					SQL:   `SELECT COUNT(*) FROM msgs_msg WHERE direction = 'O' AND text = '' AND high_priority = false AND contact_id = $1 AND optin_id = $2`,
					Args:  []any{testdb.George.ID, optIn.ID},
					Count: 1,
				},
				{ // bob has no channel+URN that supports optins
					SQL:   `SELECT COUNT(*) FROM msgs_msg WHERE direction = 'O' AND contact_id = $1`,
					Args:  []any{testdb.Bob.ID},
					Count: 0,
				},
			},
		},
	}

	handlers.RunTestCases(t, ctx, rt, tcs)

	// Cathy should have 1 batch of queued messages at high priority
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/1", testdb.FacebookChannel.UUID), 1)

	// One bulk for George
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/0", testdb.FacebookChannel.UUID), 1)
}
