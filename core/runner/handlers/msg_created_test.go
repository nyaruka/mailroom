package handlers_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestMsgCreated(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	rt.Config.AttachmentDomain = "foo.bar.com"
	defer func() { rt.Config.AttachmentDomain = "" }()

	// add a URN for Ann so we can test all urn sends
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, urns.URN("tel:+12065550100"), 10, nil)

	// delete all URNs for bob
	rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1`, testdb.Bob.ID)

	// change Dan's URN to a facebook URN and set her language to eng so that a template gets used for her
	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:12345', path='12345', scheme='facebook' WHERE contact_id = $1`, testdb.Dan.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET language='eng' WHERE id = $1`, testdb.Dan.ID)

	testdb.InsertBroadcast(t, rt, testdb.Org1, "01999b42-f414-7161-8814-fbef26d9d0d3", "eng", map[i18n.Language]string{"eng": "Cats or dogs?"}, models.NilScheduleID, nil, nil)

	runTests(t, rt, "testdata/msg_created.json")

	vc := rt.VK.Get()
	defer vc.Close()

	// Ann should have 1 batch of queued messages at high priority
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/1", testdb.TwilioChannel.UUID), 1)

	// One bulk for Cat
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/0", testdb.TwilioChannel.UUID), 1)
}

func TestMsgCreatedNewURN(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// switch our facebook channel to telegram
	rt.DB.MustExec(`UPDATE channels_channel SET channel_type = 'TG', name = 'Telegram', schemes = ARRAY['telegram'] WHERE uuid = $1`, testdb.FacebookChannel.UUID)

	runTests(t, rt, "testdata/msg_created_with_new_urn.json")
}

func TestMsgCreatedLoop(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	runTests(t, rt, "testdata/msg_created_loop.json")
}

func TestMsgCreatedTicketReply(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testdb.InsertOpenTicket(t, rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.DefaultTopic, time.Date(2025, 5, 4, 11, 30, 0, 0, time.UTC), nil)
	closed := testdb.InsertOpenTicket(t, rt, "019a0f6c-5e3b-7d1a-9b2e-3c4d5e6f7a8b", testdb.Org1, testdb.Bob, testdb.DefaultTopic, time.Date(2015, 1, 1, 12, 30, 45, 0, time.UTC), nil)
	rt.DB.MustExec(`UPDATE tickets_ticket SET status = 'C', closed_on = '2015-01-02T12:30:45Z', last_activity_on = '2015-01-02T12:30:45Z' WHERE id = $1`, closed.ID)

	runTests(t, rt, "testdata/msg_created_ticket_reply.json")
}
