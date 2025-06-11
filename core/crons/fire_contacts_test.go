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
	"github.com/nyaruka/mailroom/core/tasks/campaigns"
	"github.com/nyaruka/mailroom/core/tasks/contacts"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/assert"
)

func TestFireContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	rc := rt.RP.Get()
	defer rc.Close()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	testdb.InsertContactFire(rt, testdb.Org1, testdb.Cathy, models.ContactFireTypeWaitTimeout, "", time.Now().Add(3*time.Second), "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Cathy, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-1*time.Second), "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Cathy, models.ContactFireTypeSessionExpiration, "", time.Now().Add(-2*time.Second), "f72b48df-5f6d-4e4f-955a-f5fb29ccb97b")

	testdb.InsertContactFire(rt, testdb.Org1, testdb.Bob, models.ContactFireTypeWaitTimeout, "", time.Now().Add(3*time.Second), "4010a3b2-d1f2-42ae-9051-47d41a3ef923")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Bob, models.ContactFireTypeWaitExpiration, "", time.Now().Add(-3*time.Second), "4010a3b2-d1f2-42ae-9051-47d41a3ef923")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Bob, models.ContactFireTypeSessionExpiration, "", time.Now().Add(10*time.Second), "4010a3b2-d1f2-42ae-9051-47d41a3ef923")

	testdb.InsertContactFire(rt, testdb.Org1, testdb.George, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-time.Second), "5c1248e3-f669-4a72-83f4-a29292fdad4d")
	testdb.InsertContactFire(rt, testdb.Org1, testdb.Alexandra, models.ContactFireTypeCampaignEvent, "6789:123", time.Now().Add(-time.Second), "")
	testdb.InsertContactFire(rt, testdb.Org2, testdb.Org2Contact, models.ContactFireTypeWaitTimeout, "", time.Now().Add(-time.Second), "8edf3b3c-0081-4d31-b199-1502b3190eb7")

	cron := &crons.FireContactsCron{FetchBatchSize: 3, TaskBatchSize: 5}
	res, err := cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"wait_timeouts": 2, "wait_expires": 2, "session_expires": 1, "campaign_events": 1}, res)

	// should have created 5 throttled tasks.. unfortunately order is not guaranteed so we sort them
	var ts []*queues.Task
	for range 5 {
		task, err := tasks.ThrottledQueue.Pop(rc)
		assert.NoError(t, err)
		ts = append(ts, task)
	}
	slices.SortFunc(ts, func(a, b *queues.Task) int {
		return cmp.Or(cmp.Compare(a.OwnerID, b.OwnerID), cmp.Compare(a.Type, b.Type))
	})

	assert.Equal(t, int(testdb.Org1.ID), ts[0].OwnerID)
	assert.Equal(t, "bulk_campaign_trigger", ts[0].Type)
	assert.Equal(t, int(testdb.Org1.ID), ts[1].OwnerID)
	assert.Equal(t, "bulk_session_expire", ts[1].Type)
	assert.Equal(t, int(testdb.Org1.ID), ts[2].OwnerID)
	assert.Equal(t, "bulk_wait_expire", ts[2].Type)
	assert.Equal(t, int(testdb.Org1.ID), ts[3].OwnerID)
	assert.Equal(t, "bulk_wait_timeout", ts[3].Type)
	assert.Equal(t, int(testdb.Org2.ID), ts[4].OwnerID)
	assert.Equal(t, "bulk_wait_timeout", ts[4].Type)

	decoded1 := &campaigns.BulkCampaignTriggerTask{}
	jsonx.MustUnmarshal(ts[0].Task, decoded1)
	assert.Len(t, decoded1.ContactIDs, 1)
	assert.Equal(t, testdb.Alexandra.ID, decoded1.ContactIDs[0])
	assert.Equal(t, models.CampaignEventID(6789), decoded1.EventID)
	assert.Equal(t, 123, decoded1.FireVersion)

	decoded2 := &contacts.BulkSessionExpireTask{}
	jsonx.MustUnmarshal(ts[1].Task, decoded2)
	assert.Len(t, decoded2.SessionUUIDs, 1)
	assert.Equal(t, flows.SessionUUID("f72b48df-5f6d-4e4f-955a-f5fb29ccb97b"), decoded2.SessionUUIDs[0])

	decoded3 := &contacts.BulkWaitExpireTask{}
	jsonx.MustUnmarshal(ts[2].Task, decoded3)
	assert.Len(t, decoded3.Expirations, 2)
	assert.Equal(t, flows.SessionUUID("4010a3b2-d1f2-42ae-9051-47d41a3ef923"), decoded3.Expirations[0].SessionUUID)
	assert.Equal(t, flows.SessionUUID("f72b48df-5f6d-4e4f-955a-f5fb29ccb97b"), decoded3.Expirations[1].SessionUUID)

	decoded4 := &contacts.BulkWaitTimeoutTask{}
	jsonx.MustUnmarshal(ts[3].Task, decoded4)
	assert.Len(t, decoded4.Timeouts, 1)
	assert.Equal(t, flows.SessionUUID("5c1248e3-f669-4a72-83f4-a29292fdad4d"), decoded4.Timeouts[0].SessionUUID)

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM contacts_contactfire`).Returns(3) // only 3 fires in the future left

	res, err = cron.Run(ctx, rt)
	assert.NoError(t, err)
	assert.Equal(t, map[string]any{"wait_timeouts": 0, "wait_expires": 0, "session_expires": 0, "campaign_events": 0}, res)
}
