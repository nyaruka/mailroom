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
	group := testdb.InsertContactGroup(t, rt, testdb.Org1, "5f1f2b3c-2e7d-4c55-9d0b-7a4f9b0c1e22", "Newbies", `name = "Newbie"`)

	models.FlushCache()

	err = tasks.QueueContact(ctx, rt, testdb.Org1.ID, newbie.ID, &ctasks.ContactCreated{ChannelID: testdb.TwilioChannel.ID})
	require.NoError(t, err)

	queued, err := rt.Queues.Realtime.Pop(ctx, vc)
	require.NoError(t, err)
	require.NotNil(t, queued)

	err = tasks.Perform(ctx, rt, queued)
	require.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contactgroup_contacts WHERE contactgroup_id = $1 AND contact_id = $2`, group.ID, newbie.ID).Returns(1)
}
