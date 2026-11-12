package ctasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactCreated(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	task, err := ctasks.ReadTask("contact_created", []byte(`{"channel_id": 10000}`))
	require.NoError(t, err)
	assert.Equal(t, &ctasks.ContactCreated{ChannelID: 10000}, task)

	task, err = ctasks.ReadTask("contact_created", []byte(`{}`))
	require.NoError(t, err)
	assert.Equal(t, &ctasks.ContactCreated{}, task)

	newbie := testdb.InsertContact(t, rt, testdb.Org1, "4a0d7b9b-3a5e-4b8b-9a63-6c1d4d2f8a11", "Newbie", "eng", models.ContactStatusActive)
	blocked := testdb.InsertContact(t, rt, testdb.Org1, "9c3e1a52-7b4f-4d2e-8f61-3a5b7c9d0e33", "Newbie", "eng", models.ContactStatusBlocked)
	matching := testdb.InsertContactGroup(t, rt, testdb.Org1, "5f1f2b3c-2e7d-4c55-9d0b-7a4f9b0c1e22", "Newbies", `name = "Newbie"`)
	nonMatching := testdb.InsertContactGroup(t, rt, testdb.Org1, "6a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c55", "Oldies", `name = "Oldie"`)

	models.FlushCache()

	perform := func(contact *testdb.Contact) {
		err := tasks.QueueContact(ctx, rt, testdb.Org1.ID, contact.ID, &ctasks.ContactCreated{ChannelID: testdb.TwilioChannel.ID})
		require.NoError(t, err)

		queued, err := rt.Queues.Realtime.Pop(ctx, vc)
		require.NoError(t, err)
		require.NotNil(t, queued)

		err = tasks.Perform(ctx, rt, queued)
		require.NoError(t, err)
	}

	assertMemberships := func(contact *testdb.Contact, group *testdb.Group, expected int) {
		assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1 AND contact_id = $2`, group.ID, contact.ID).Returns(expected)
	}

	perform(newbie)
	assertMemberships(newbie, matching, 1)
	assertMemberships(newbie, nonMatching, 0)

	// running again is a noop
	perform(newbie)
	assertMemberships(newbie, matching, 1)
	assertMemberships(newbie, nonMatching, 0)

	// blocked contacts aren't added to query groups
	perform(blocked)
	assertMemberships(blocked, matching, 0)
}

func TestContactCreatedFlagAliases(t *testing.T) {
	for _, data := range []string{`{"new_contact": true}`, `{"contact_created": true}`} {
		task, err := ctasks.ReadTask("msg_received", []byte(data))
		require.NoError(t, err)
		mr := task.(*ctasks.MsgReceived)
		assert.True(t, mr.NewContact || mr.ContactCreated, "msg_received: %s", data)

		task, err = ctasks.ReadTask("event_received", []byte(data))
		require.NoError(t, err)
		er := task.(*ctasks.EventReceived)
		assert.True(t, er.NewContact || er.ContactCreated, "event_received: %s", data)
	}
}
