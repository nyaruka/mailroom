package campaigns_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	_ "github.com/nyaruka/mailroom/core/handlers"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks/campaigns"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/nyaruka/redisx/assertredis"
	"github.com/stretchr/testify/assert"
)

func TestBulkCampaignTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	defer random.SetGenerator(random.DefaultGenerator)
	random.SetGenerator(random.NewSeededGenerator(123))

	rc := rt.RP.Get()
	defer rc.Close()

	// create a waiting session for Cathy
	testdata.InsertWaitingSession(rt, testdata.Cathy, models.FlowTypeVoice, testdata.IVRFlow, models.NilCallID)
	rt.DB.MustExec(`UPDATE contacts_contact SET current_flow_id = $2 WHERE id = $1`, testdata.Cathy.ID, testdata.IVRFlow.ID)

	// create task for event #3 (Pick A Number, start mode SKIP)
	task := &campaigns.BulkCampaignTriggerTask{EventID: testdata.RemindersEvent3.ID, ContactIDs: []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandria.ID}}

	oa := testdata.Org1.Load(rt)
	err := task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.IVRFlow) // event skipped cathy because she has a waiting session
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.PickANumber)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandria, testdata.PickANumber)

	// check we recorded recent triggers for this event
	assertredis.Keys(t, rc, "recent_campaign_fires:*", []string{"recent_campaign_fires:10002"})
	assertredis.ZRange(t, rc, "recent_campaign_fires:10002", 0, -1, []string{"6MBPV0gqT9|10001", "PLQQFoOgV9|10003"})

	// create task for event #2 (single message, start mode PASSIVE)
	task = &campaigns.BulkCampaignTriggerTask{EventID: testdata.RemindersEvent2.ID, ContactIDs: []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandria.ID}}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// everyone still in the same flows
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.IVRFlow)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.PickANumber)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandria, testdata.PickANumber)

	// but also have a completed session for the single message flow
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'C'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'C'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'C'`, testdata.Alexandria.ID).Returns(1)

	// check we recorded recent triggers for this event
	assertredis.Keys(t, rc, "recent_campaign_fires:*", []string{"recent_campaign_fires:10001", "recent_campaign_fires:10002"})
	assertredis.ZRange(t, rc, "recent_campaign_fires:10001", 0, -1, []string{"/cgnkcW6vA|10001", "YAnU/8BkiR|10000", "uI8bPiuaeA|10003"})
	assertredis.ZRange(t, rc, "recent_campaign_fires:10002", 0, -1, []string{"6MBPV0gqT9|10001", "PLQQFoOgV9|10003"})

	// create task for event #1 (Favorites, start mode INTERRUPT)
	task = &campaigns.BulkCampaignTriggerTask{EventID: testdata.RemindersEvent1.ID, ContactIDs: []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandria.ID}}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// everyone should be in campaign event flow
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandria, testdata.Favorites)

	// and their previous waiting sessions will have been interrupted
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Alexandria.ID).Returns(1)

	// test task when campaign event has been deleted
	rt.DB.MustExec(`UPDATE campaigns_campaignevent SET is_active = FALSE WHERE id = $1`, testdata.RemindersEvent1.ID)
	models.FlushCache()
	oa = testdata.Org1.Load(rt)

	task = &campaigns.BulkCampaignTriggerTask{EventID: testdata.RemindersEvent1.ID, ContactIDs: []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandria.ID}}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// task should be a noop, no new sessions created
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandria, testdata.Favorites)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Alexandria.ID).Returns(1)

	// test task when flow has been deleted
	rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, testdata.PickANumber.ID)
	models.FlushCache()
	oa = testdata.Org1.Load(rt)

	task = &campaigns.BulkCampaignTriggerTask{EventID: testdata.RemindersEvent3.ID, ContactIDs: []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandria.ID}}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// task should be a noop, no new sessions created
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandria, testdata.Favorites)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Alexandria.ID).Returns(1)
}
