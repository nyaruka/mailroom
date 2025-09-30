package handlers_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestMsgCreated(t *testing.T) {
	_, rt := testsuite.Runtime(t)

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

	testdb.InsertBroadcast(t, rt, testdb.Org1, "01999b42-f414-7161-8814-fbef26d9d0d3", "eng", map[i18n.Language]string{"eng": "Cats or dogs?"}, nil, models.NilScheduleID, nil, nil)

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

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// switch our twitter channel to telegram
	rt.DB.MustExec(`UPDATE channels_channel SET channel_type = 'TG', name = 'Telegram', schemes = ARRAY['telegram'] WHERE uuid = $1`, testdb.FacebookChannel.UUID)

	// give Cat a URN that Bob will steal
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Cat, urns.URN("telegram:67890"), 1, nil)

	runTests(t, rt, "testdata/msg_created_with_new_urn.json")
}
