package campaigns_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
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
	testdata.InsertWaitingSession(rt, testdata.Org1, testdata.Cathy, models.FlowTypeVoice, testdata.IVRFlow, models.NilCallID)

	// create task for event #3 (Pick A Number, start mode SKIP)
	task := &campaigns.BulkCampaignTriggerTask{
		EventID:     testdata.RemindersEvent3.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandra.ID},
	}

	oa := testdata.Org1.Load(rt)
	err := task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.IVRFlow) // event skipped cathy because she has a waiting session
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.PickANumber)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandra, testdata.PickANumber)

	// check we recorded recent triggers for this event
	assertredis.Keys(t, rc, "recent_campaign_fires:*", []string{"recent_campaign_fires:10002"})
	assertredis.ZRange(t, rc, "recent_campaign_fires:10002", 0, -1, []string{"BPV0gqT9PL|10001", "QQFoOgV99A|10003"})

	// create task for event #2 (single message, start mode PASSIVE)
	task = &campaigns.BulkCampaignTriggerTask{
		EventID:     testdata.RemindersEvent2.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandra.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// everyone still in the same flows
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.IVRFlow)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.PickANumber)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandra, testdata.PickANumber)

	// and should have a queued message
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Hi Cathy, it is time to consult with your patients.' AND status = 'Q'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Hi Bob, it is time to consult with your patients.' AND status = 'Q'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Hi Alexandra, it is time to consult with your patients.' AND status = 'Q'`).Returns(1)

	// check we recorded recent triggers for this event
	assertredis.Keys(t, rc, "recent_campaign_fires:*", []string{"recent_campaign_fires:10001", "recent_campaign_fires:10002"})
	assertredis.ZRange(t, rc, "recent_campaign_fires:10001", 0, -1, []string{"vWOxKKbX2M|10001", "sZZ/N3THKK|10000", "LrT60Tr9/c|10003"})
	assertredis.ZRange(t, rc, "recent_campaign_fires:10002", 0, -1, []string{"BPV0gqT9PL|10001", "QQFoOgV99A|10003"})

	// create task for event #1 (Favorites, start mode INTERRUPT)
	task = &campaigns.BulkCampaignTriggerTask{
		EventID:     testdata.RemindersEvent1.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandra.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// everyone should be in campaign event flow
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandra, testdata.Favorites)

	// and their previous waiting sessions will have been interrupted
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Alexandra.ID).Returns(1)

	// test task when campaign event has been deleted
	rt.DB.MustExec(`UPDATE campaigns_campaignevent SET is_active = FALSE WHERE id = $1`, testdata.RemindersEvent1.ID)
	models.FlushCache()
	oa = testdata.Org1.Load(rt)

	task = &campaigns.BulkCampaignTriggerTask{
		EventID:     testdata.RemindersEvent1.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandra.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// task should be a noop, no new sessions created
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandra, testdata.Favorites)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Alexandra.ID).Returns(1)

	// test task when flow has been deleted
	rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, testdata.PickANumber.ID)
	models.FlushCache()
	oa = testdata.Org1.Load(rt)

	task = &campaigns.BulkCampaignTriggerTask{
		EventID:     testdata.RemindersEvent3.ID,
		ContactIDs:  []models.ContactID{testdata.Bob.ID, testdata.Cathy.ID, testdata.Alexandra.ID},
		FireVersion: 1,
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// task should be a noop, no new sessions created
	testsuite.AssertContactInFlow(t, rt, testdata.Cathy, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Bob, testdata.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdata.Alexandra, testdata.Favorites)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Bob.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Cathy.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_id = $1 AND status = 'I'`, testdata.Alexandra.ID).Returns(1)
}
