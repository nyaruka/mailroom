package handlers_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestOptinRequested(t *testing.T) {
	_, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	testdb.InsertOptIn(t, rt, testdb.Org1, "45aec4dd-945f-4511-878f-7d8516fbd336", "Jokes")

	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:12345', scheme='facebook', path='12345' WHERE contact_id = $1`, testdb.Ann.ID)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'facebook:23456', scheme='facebook', path='23456' WHERE contact_id = $1`, testdb.Cat.ID)

	oa := testdb.Org1.Load(t, rt)
	ch := oa.ChannelByUUID("0f661e8b-ea9d-4bd3-9953-d368340acf91")
	assert.Equal(t, models.ChannelType("FBA"), ch.Type())
	assert.Equal(t, []assets.ChannelFeature{assets.ChannelFeatureOptIns}, ch.Features())

	runTests(t, rt, "testdata/optin_requested.json")

	// Ann should have 1 batch of queued messages at high priority
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/1", testdb.FacebookChannel.UUID), 1)

	// One bulk for Cat
	assertvk.ZCard(t, vc, fmt.Sprintf("msgs:%s|10/0", testdb.FacebookChannel.UUID), 1)
}
