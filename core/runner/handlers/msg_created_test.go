package handlers_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/actions"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestMsgCreated(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	rt.Config.AttachmentDomain = "foo.bar.com"
	defer func() { rt.Config.AttachmentDomain = "" }()

	// add a URN for cathy so we can test all urn sends
	testdb.InsertContactURN(rt, testdb.Org1, testdb.Cathy, urns.URN("tel:+12065551212"), 10, nil)

	// delete all URNs for bob
	rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1`, testdb.Bob.ID)

	// change alexandrias URN to a facebook URN and set her language to eng so that a template gets used for her
	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:12345', path='12345', scheme='facebook' WHERE contact_id = $1`, testdb.Alexandra.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET language='eng' WHERE id = $1`, testdb.Alexandra.ID)

	msg1 := testdb.InsertIncomingMsg(rt, testdb.Org1, testdb.TwilioChannel, testdb.Cathy, "start", models.MsgStatusPending)

	templateAction := actions.NewSendMsg(flows.NewActionUUID(), "Template time", nil, nil, false)
	templateAction.Template = assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue")
	templateAction.TemplateVariables = []string{"@contact.name", "tooth"}

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Cathy: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "Hello World", nil, []string{"yes", "no"}, true),
				},
				testdb.George: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "Hello Attachments", []string{"image/png:/images/image1.png"}, nil, true),
				},
				testdb.Bob: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "No URNs", nil, nil, false),
				},
				testdb.Alexandra: []flows.Action{
					templateAction,
				},
			},
			Msgs: ContactMsgMap{
				testdb.Cathy: msg1,
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL:   `SELECT COUNT(*) FROM msgs_msg WHERE text='Hello World' AND contact_id = $1 AND quick_replies[1] = 'yes' AND quick_replies[2] = 'no' AND high_priority = TRUE`,
					Args:  []any{testdb.Cathy.ID},
					Count: 2,
				},
				{
					SQL:   "SELECT COUNT(*) FROM msgs_msg WHERE text='Hello Attachments' AND contact_id = $1 AND attachments[1] = $2 AND status = 'Q' AND high_priority = FALSE",
					Args:  []any{testdb.George.ID, "image/png:https://foo.bar.com/images/image1.png"},
					Count: 1,
				},
				{
					SQL:   "SELECT COUNT(*) FROM msgs_msg WHERE contact_id=$1 AND STATUS = 'F' AND failed_reason = 'D';",
					Args:  []any{testdb.Bob.ID},
					Count: 1,
				},
				{
					SQL: "SELECT COUNT(*) FROM msgs_msg WHERE contact_id = $1 AND text = $2 AND direction = 'O' AND status = 'Q' AND channel_id = $3 AND templating->'template'->>'name' = 'revive_issue'",
					Args: []any{
						testdb.Alexandra.ID,
						`Hi Alexandra, are you still experiencing problems with tooth?`,
						testdb.FacebookChannel.ID,
					},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Cathy.UUID:     {"run_started", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "run_ended"},
				testdb.George.UUID:    {"run_started", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs)

	// Cathy should have 1 batch of queued messages at high priority
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/1", testdb.TwilioChannel.UUID), 1)

	// One bulk for George
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/0", testdb.TwilioChannel.UUID), 1)
}

func TestMsgCreatedNewURN(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// switch our twitter channel to telegram
	telegramUUID := testdb.FacebookChannel.UUID
	telegramID := testdb.FacebookChannel.ID
	rt.DB.MustExec(
		`UPDATE channels_channel SET channel_type = 'TG', name = 'Telegram', schemes = ARRAY['telegram'] WHERE uuid = $1`,
		telegramUUID,
	)

	// give George a URN that Bob will steal
	testdb.InsertContactURN(rt, testdb.Org1, testdb.George, urns.URN("telegram:67890"), 1, nil)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				// brand new URN on Cathy
				testdb.Cathy: []flows.Action{
					actions.NewAddContactURN(flows.NewActionUUID(), "telegram", "12345"),
					actions.NewSetContactChannel(flows.NewActionUUID(), assets.NewChannelReference(telegramUUID, "telegram")),
					actions.NewSendMsg(flows.NewActionUUID(), "Cathy Message", nil, nil, false),
				},

				// Bob is stealing a URN previously assigned to George
				testdb.Bob: []flows.Action{
					actions.NewAddContactURN(flows.NewActionUUID(), "telegram", "67890"),
					actions.NewSetContactChannel(flows.NewActionUUID(), assets.NewChannelReference(telegramUUID, "telegram")),
					actions.NewSendMsg(flows.NewActionUUID(), "Bob Message", nil, nil, false),
				},
			},
			SQLAssertions: []SQLAssertion{
				{
					SQL: `
					SELECT 
					  COUNT(*) 
					FROM 
					  msgs_msg m 
					  JOIN contacts_contacturn u ON m.contact_urn_id = u.id
					WHERE 
					  m.text='Cathy Message' AND 
					  m.contact_id = $1 AND 
					  m.status = 'Q' AND
					  u.identity = $2 AND
					  m.channel_id = $3 AND
					  u.channel_id IS NULL`,
					Args:  []any{testdb.Cathy.ID, "telegram:12345", telegramID},
					Count: 1,
				},
				{
					SQL: `
					SELECT 
					  COUNT(*) 
					FROM 
					  msgs_msg m 
					  JOIN contacts_contacturn u ON m.contact_urn_id = u.id
					WHERE 
					  m.text='Bob Message' AND 
					  m.contact_id = $1 AND 
					  m.status = 'Q' AND
					  u.identity = $2 AND
					  m.channel_id = $3 AND
					  u.channel_id IS NULL`,
					Args:  []any{testdb.Bob.ID, "telegram:67890", telegramID},
					Count: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Cathy.UUID:     {"run_started", "contact_urns_changed", "contact_urns_changed", "run_ended"},
				testdb.Bob.UUID:       {"run_started", "contact_urns_changed", "contact_urns_changed", "run_ended"},
				testdb.George.UUID:    {"run_started", "run_ended"},
				testdb.Alexandra.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs)
}
