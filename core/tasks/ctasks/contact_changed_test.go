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

func TestContactChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	// this test pops tasks without marking them done, inflating the org's active count in the fair queue,
	// so it needs to reset valkey for the tests which come after it
	defer testsuite.Reset(t, rt, testsuite.ResetDynamo)

	type urnRow struct {
		Identity  string            `db:"identity"`
		ChannelID *models.ChannelID `db:"channel_id"`
	}

	var shell *testdb.Contact

	tcs := []struct {
		label       string
		preHook     func()
		postHook    func(t *testing.T)
		contact     *testdb.Contact
		channel     *testdb.Channel
		newURN      *ctasks.NewURNSpec
		expectedURN []urnRow
	}{
		{
			label:   "append new URN saves channel affinity",
			contact: testdb.Bob,
			channel: testdb.TwilioChannel,
			newURN: &ctasks.NewURNSpec{
				Value:  "telegram:98765",
				Action: "append",
			},
			expectedURN: []urnRow{
				{Identity: "tel:+16055742222", ChannelID: nil},
				{Identity: "telegram:98765", ChannelID: &testdb.TwilioChannel.ID},
			},
		},
		{
			label: "append duplicate URN",
			preHook: func() {
				rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1 AND scheme = 'telegram'`, testdb.Bob.ID)
				testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Bob, "telegram:98765", 999, nil)
			},
			contact: testdb.Bob,
			channel: testdb.TwilioChannel,
			newURN: &ctasks.NewURNSpec{
				Value:  "telegram:98765",
				Action: "append",
			},
			// telegram URN already existed without a channel, no modification event emitted
			expectedURN: []urnRow{
				{Identity: "tel:+16055742222", ChannelID: nil},
				{Identity: "telegram:98765", ChannelID: nil},
			},
		},
		{
			label: "append WhatsApp BSUID URN owned by URN-less shell contact claims it",
			preHook: func() {
				rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1 AND scheme != 'tel'`, testdb.Bob.ID)

				// create a shell contact whose only URN is the BSUID
				shell = testdb.InsertContact(t, rt, testdb.Org1, "778cb7bb-e6ef-4786-b8d0-a7e29e7cd7cd", "Shell", "eng", models.ContactStatusActive)
				testdb.InsertContactURN(t, rt, testdb.Org1, shell, "whatsapp:US.SHELL1", 1000, nil)
			},
			contact: testdb.Bob,
			channel: testdb.TwilioChannel,
			newURN: &ctasks.NewURNSpec{
				Value:  "whatsapp:US.SHELL1",
				Action: "append",
			},
			expectedURN: []urnRow{
				{Identity: "tel:+16055742222", ChannelID: nil},
				{Identity: "whatsapp:US.SHELL1", ChannelID: &testdb.TwilioChannel.ID},
			},
			postHook: func(t *testing.T) {
				// shell contact is left without URNs
				assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE contact_id = $1`, shell.ID).Returns(0)
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			models.FlushCache()

			if tc.preHook != nil {
				tc.preHook()
			}

			task := &ctasks.ContactChanged{
				ChannelID: tc.channel.ID,
				NewURN:    tc.newURN,
			}

			err := tasks.QueueContact(ctx, rt, testdb.Org1.ID, tc.contact.ID, task)
			require.NoError(t, err)

			queued, err := rt.Queues.Realtime.Pop(ctx, vc)
			require.NoError(t, err)
			require.NotNil(t, queued)

			err = tasks.Perform(ctx, rt, queued)
			require.NoError(t, err)

			var urnRows []urnRow
			err = rt.DB.Select(&urnRows, `SELECT identity, channel_id FROM contacts_contacturn WHERE contact_id = $1 ORDER BY priority DESC`, tc.contact.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedURN, urnRows)

			if tc.postHook != nil {
				tc.postHook(t)
			}
		})
	}
}
