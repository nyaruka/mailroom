package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWebChatVisitor(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	wch := testdb.InsertChannel(t, rt, testdb.Org1, models.ChannelTypeWebChat, "Web Chat", "", []string{"webchat"}, "SR", map[string]any{})
	contact := testdb.InsertContact(t, rt, testdb.Org1, "c5a1e1f7-3b4d-4f2e-8a6c-9d0b7e2f4a13", "", "", models.ContactStatusActive)

	// unknown chat isn't cached, so the visitor is found once their URN exists
	v, err := models.GetWebChatVisitor(ctx, wch.UUID, "AbCdEf0123456789GhIjKlMn")
	assert.NoError(t, err)
	assert.Nil(t, v)

	urnID := testdb.InsertContactURN(t, rt, testdb.Org1, contact, urns.URN("webchat:AbCdEf0123456789GhIjKlMn"), 1000, nil)

	// but it must be attached to the channel
	v, err = models.GetWebChatVisitor(ctx, wch.UUID, "AbCdEf0123456789GhIjKlMn")
	assert.NoError(t, err)
	assert.Nil(t, v)

	rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $2 WHERE id = $1`, urnID, wch.ID)

	v, err = models.GetWebChatVisitor(ctx, wch.UUID, "AbCdEf0123456789GhIjKlMn")
	require.NoError(t, err)
	assert.Equal(t, &models.WebChatVisitor{OrgID: testdb.Org1.ID, ContactUUID: contact.UUID}, v)

	// a found visitor is cached
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, contact.ID)

	v, err = models.GetWebChatVisitor(ctx, wch.UUID, "AbCdEf0123456789GhIjKlMn")
	require.NoError(t, err)
	assert.NotNil(t, v)

	models.FlushCache()

	v, err = models.GetWebChatVisitor(ctx, wch.UUID, "AbCdEf0123456789GhIjKlMn")
	assert.NoError(t, err)
	assert.Nil(t, v)

	// only webchat channels have visitors
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Bob, urns.URN("webchat:ZyXwVu9876543210TsRqPoNm"), 1000, nil)
	rt.DB.MustExec(`UPDATE contacts_contacturn SET channel_id = $1 WHERE identity = 'webchat:ZyXwVu9876543210TsRqPoNm'`, testdb.TwilioChannel.ID)

	v, err = models.GetWebChatVisitor(ctx, testdb.TwilioChannel.UUID, "ZyXwVu9876543210TsRqPoNm")
	assert.NoError(t, err)
	assert.Nil(t, v)
}
