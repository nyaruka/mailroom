package ctasks_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContactChanged(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetDynamo|testsuite.ResetElastic)

	tcs := []struct {
		label       string
		preHook     func()
		contact     *testdb.Contact
		newURN      *ctasks.NewURNSpec
		expectedURN []string
		expectedErr string
	}{
		{
			label:   "append new URN",
			contact: testdb.Bob,
			newURN: &ctasks.NewURNSpec{
				Value:  "telegram:98765",
				Action: "append",
			},
			expectedURN: []string{"tel:+16055742222", "telegram:98765"},
		},
		{
			label: "append duplicate URN",
			preHook: func() {
				rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1 AND scheme = 'telegram'`, testdb.Bob.ID)
				testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Bob, "telegram:98765", 999, nil)
			},
			contact: testdb.Bob,
			newURN: &ctasks.NewURNSpec{
				Value:  "telegram:98765",
				Action: "append",
			},
			expectedURN: []string{"tel:+16055742222", "telegram:98765"},
		},
		{
			label:   "unsupported action errors",
			contact: testdb.Bob,
			newURN: &ctasks.NewURNSpec{
				Value:  "telegram:98765",
				Action: "prepend",
			},
			expectedErr: "unsupported new_urn action: prepend",
		},
		{
			label:   "empty URN value errors",
			contact: testdb.Bob,
			newURN: &ctasks.NewURNSpec{
				Value:  "",
				Action: "append",
			},
			expectedErr: "new_urn value is required",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			models.FlushCache()

			if tc.preHook != nil {
				tc.preHook()
			}

			task := &ctasks.ContactChanged{
				NewURN: tc.newURN,
			}

			if tc.expectedErr != "" {
				oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
				require.NoError(t, err)

				err = ctasks.Perform(ctx, rt, oa, tc.contact.ID, task)
				assert.ErrorContains(t, err, tc.expectedErr)
				return
			}

			err := tasks.QueueContact(ctx, rt, testdb.Org1.ID, tc.contact.ID, task)
			require.NoError(t, err)

			queued, err := rt.Queues.Realtime.Pop(ctx, vc)
			require.NoError(t, err)
			require.NotNil(t, queued)

			err = tasks.Perform(ctx, rt, queued)
			require.NoError(t, err)

			var urnIdentities []string
			err = rt.DB.Select(&urnIdentities, `SELECT identity FROM contacts_contacturn WHERE contact_id = $1 ORDER BY priority DESC`, tc.contact.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedURN, urnIdentities)
		})
	}
}
