package handlers_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
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

	// add a URN for Ann so we can test all urn sends
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, urns.URN("tel:+12065551212"), 10, nil)

	// delete all URNs for bob
	rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1`, testdb.Bob.ID)

	// change Dan's URN to a facebook URN and set her language to eng so that a template gets used for her
	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:12345', path='12345', scheme='facebook' WHERE contact_id = $1`, testdb.Dan.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET language='eng' WHERE id = $1`, testdb.Dan.ID)

	msg1 := testdb.InsertIncomingMsg(t, rt, testdb.Org1, testdb.TwilioChannel, testdb.Ann, "start", models.MsgStatusPending)

	templateAction := actions.NewSendMsg(flows.NewActionUUID(), "Template time", nil, nil, false)
	templateAction.Template = assets.NewTemplateReference("9c22b594-fcab-4b29-9bcb-ce4404894a80", "revive_issue")
	templateAction.TemplateVariables = []string{"@contact.name", "tooth"}

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				testdb.Ann.UUID: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "Hello World", nil, []string{"yes", "no"}, true),
				},
				testdb.Cat.UUID: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "Hello Attachments", []string{"image/png:/images/image1.png"}, nil, true),
				},
				testdb.Bob.UUID: []flows.Action{
					actions.NewSendMsg(flows.NewActionUUID(), "No URNs", nil, nil, false),
				},
				testdb.Dan.UUID: []flows.Action{
					templateAction,
				},
			},
			Msgs: ContactMsgMap{
				testdb.Ann.UUID: msg1,
			},
			DBAssertions: []assertdb.Assert{
				{
					Query:   `SELECT COUNT(*) FROM msgs_msg WHERE text='Hello World' AND contact_id = $1 AND quick_replies[1] = 'yes' AND quick_replies[2] = 'no' AND high_priority = TRUE`,
					Args:    []any{testdb.Ann.ID},
					Returns: 2,
				},
				{
					Query:   "SELECT COUNT(*) FROM msgs_msg WHERE text='Hello Attachments' AND contact_id = $1 AND attachments[1] = $2 AND status = 'Q' AND high_priority = FALSE",
					Args:    []any{testdb.Cat.ID, "image/png:https://foo.bar.com/images/image1.png"},
					Returns: 1,
				},
				{
					Query:   "SELECT COUNT(*) FROM msgs_msg WHERE contact_id=$1 AND STATUS = 'F' AND failed_reason = 'D';",
					Args:    []any{testdb.Bob.ID},
					Returns: 1,
				},
				{
					Query: "SELECT COUNT(*) FROM msgs_msg WHERE contact_id = $1 AND text = $2 AND direction = 'O' AND status = 'Q' AND channel_id = $3 AND templating->'template'->>'name' = 'revive_issue'",
					Args: []any{
						testdb.Dan.ID,
						`Hi Dan, are you still experiencing problems with tooth?`,
						testdb.FacebookChannel.ID,
					},
					Returns: 1,
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

	runTestCases(t, ctx, rt, tcs)

	// Ann should have 1 batch of queued messages at high priority
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/1", testdb.TwilioChannel.UUID), 1)

	// One bulk for Cat
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

	// give Cat a URN that Bob will steal
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Cat, urns.URN("telegram:67890"), 1, nil)

	tcs := []TestCase{
		{
			Actions: ContactActionMap{
				// brand new URN on Ann
				testdb.Ann.UUID: []flows.Action{
					actions.NewAddContactURN(flows.NewActionUUID(), "telegram", "12345"),
					actions.NewSetContactChannel(flows.NewActionUUID(), assets.NewChannelReference(telegramUUID, "telegram")),
					actions.NewSendMsg(flows.NewActionUUID(), "Ann Message", nil, nil, false),
				},

				// Bob is stealing a URN previously assigned to Cat
				testdb.Bob.UUID: []flows.Action{
					actions.NewAddContactURN(flows.NewActionUUID(), "telegram", "67890"),
					actions.NewSetContactChannel(flows.NewActionUUID(), assets.NewChannelReference(telegramUUID, "telegram")),
					actions.NewSendMsg(flows.NewActionUUID(), "Bob Message", nil, nil, false),
				},
			},
			DBAssertions: []assertdb.Assert{
				{
					Query: `
					SELECT 
					  COUNT(*) 
					FROM 
					  msgs_msg m 
					  JOIN contacts_contacturn u ON m.contact_urn_id = u.id
					WHERE 
					  m.text='Ann Message' AND 
					  m.contact_id = $1 AND 
					  m.status = 'Q' AND
					  u.identity = $2 AND
					  m.channel_id = $3 AND
					  u.channel_id IS NULL`,
					Args:    []any{testdb.Ann.ID, "telegram:12345", telegramID},
					Returns: 1,
				},
				{
					Query: `
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
					Args:    []any{testdb.Bob.ID, "telegram:67890", telegramID},
					Returns: 1,
				},
			},
			PersistedEvents: map[flows.ContactUUID][]string{
				testdb.Ann.UUID: {"run_started", "contact_urns_changed", "contact_urns_changed", "run_ended"},
				testdb.Bob.UUID: {"run_started", "contact_urns_changed", "contact_urns_changed", "run_ended"},
				testdb.Cat.UUID: {"run_started", "run_ended"},
				testdb.Dan.UUID: {"run_started", "run_ended"},
			},
		},
	}

	runTestCases(t, ctx, rt, tcs)
}
