package crons_test

import (
	"cmp"
	"slices"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/assert"
)

func TestFireContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Ann, models.ContactFireTypeWaitTimeout, "", time.Now().Add(3*time.Second), "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Ann, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-1*time.Second), "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Ann, models.ContactFireTypeSessionExpiration, "", time.Now().Add(-2*time.Second), "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b")

	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Bob, models.ContactFireTypeWaitTimeout, "", time.Now().Add(3*time.Second), "4010a3b2-d1f2-42ae-9051-47d41a3ef923")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Bob, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-3*time.Second), "4010a3b2-d1f2-42ae-9051-47d41a3ef923")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Bob, models.ContactFireTypeSessionExpiration, "", time.Now().Add(10*time.Second), "4010a3b2-d1f2-42ae-9051-47d41a3ef923")

	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Cat, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-time.Second), "5c1248e3-f669-4a72-83f4-a29292fdad4d")
	testdb.InsertContactFire(t, rt, testdb.Org1, testdb.Dan, models.ContactFireTypeCampaignPoint, "6789:123", time.Now().Add(-time.Second), "")
	testdb.InsertContactFire(t, rt, testdb.Org2, testdb.Org2Contact, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-time.Second), "8edf3b3c-0081-4d31-b199-1502b3190eb7")

	cron := &crons.FireContactsCron{FetchBatchSize: 3, TaskBatchSize: 5}
	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"wait_timeouts": 2, "wait_expires": 2, "session_expires": 1, "campaign_points": 1}, res)

	// should have created 4 tasks in throttled queue.. unfortunately order is not guaranteed so we sort them
	var ts []*queues.Task
	for range 4 {
		task, err := rt.Queues.Throttled.Pop(ctx, vc)
		assert.NoError(t, err)
		ts = append(ts, task)
	}
	slices.SortFunc(ts, func(a, b *queues.Task) int {
		return cmp.Or(cmp.Compare(a.OwnerID, b.OwnerID), cmp.Compare(a.Type, b.Type))
	})

	// and one in the batch queue
	task, err := rt.Queues.Batch.Pop(ctx, vc)
	assert.NoError(t, err)
	ts = append(ts, task)

	assert.Equal(t, int(testdb.Org1.ID), ts[0].OwnerID)
	assert.Equal(t, "bulk_campaign_trigger", ts[0].Type)
	assert.Equal(t, int(testdb.Org1.ID), ts[1].OwnerID)
	assert.Equal(t, "bulk_wait_expire", ts[1].Type)
	assert.Equal(t, int(testdb.Org1.ID), ts[2].OwnerID)
	assert.Equal(t, "bulk_wait_timeout", ts[2].Type)
	assert.Equal(t, int(testdb.Org2.ID), ts[3].OwnerID)
	assert.Equal(t, "bulk_wait_timeout", ts[3].Type)
	assert.Equal(t, int(testdb.Org1.ID), ts[4].OwnerID)
	assert.Equal(t, "interrupt_session_batch", ts[4].Type)

	decoded1 := &tasks.BulkCampaignTrigger{}
	jsonx.MustUnmarshal(ts[0].Task, decoded1)
	assert.Len(t, decoded1.ContactIDs, 1)
	assert.Equal(t, testdb.Dan.ID, decoded1.ContactIDs[0])
	assert.Equal(t, models.PointID(6789), decoded1.PointID)
	assert.Equal(t, 123, decoded1.FireVersion)

	decoded2 := &tasks.BulkWaitExpire{}
	jsonx.MustUnmarshal(ts[1].Task, decoded2)
	assert.Len(t, decoded2.Expirations, 2)
	assert.Equal(t, flows.SessionUUID("4010a3b2-d1f2-42ae-9051-47d41a3ef923"), decoded2.Expirations[0].SessionUUID)
	assert.Equal(t, flows.SessionUUID("f72b48df-5f6d-4e4f-955a-f5fb29ccb97b"), decoded2.Expirations[1].SessionUUID)

	decoded3 := &tasks.BulkWaitTimeout{}
	jsonx.MustUnmarshal(ts[2].Task, decoded3)
	assert.Len(t, decoded3.Timeouts, 1)
	assert.Equal(t, flows.SessionUUID("5c1248e3-f669-4a72-83f4-a29292fdad4d"), decoded3.Timeouts[0].SessionUUID)

	decoded4 := &tasks.InterruptSessionBatch{}
	jsonx.MustUnmarshal(ts[4].Task, decoded4)
	assert.Equal(t, []models.SessionRef{{UUID: "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b", ContactID: testdb.Ann.ID}}, decoded4.Sessions)
	assert.Equal(t, flows.SessionStatusExpired, decoded4.Status)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`).Returns(3) // only 3 fires in the future left

	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"wait_timeouts": 0, "wait_expires": 0, "session_expires": 0, "campaign_points": 0}, res)
}
