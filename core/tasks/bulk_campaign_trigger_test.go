package tasks_test

import (
	"testing"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/vkutil/assertvk"
	"github.com/stretchr/testify/assert"
)

func TestBulkCampaignTrigger(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	defer random.SetGenerator(random.DefaultGenerator)
	random.SetGenerator(random.NewSeededGenerator(123))

	vc := rt.VK.Get()
	defer vc.Close()

	// create a waiting session for Ann
	testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Ann, models.FlowTypeVoice, nil, testdb.IVRFlow)

	// create task for event #3 (Pick A Number, start mode SKIP)
	task := &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint3.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Bob.ID, testdb.Ann.ID, testdb.Dan.ID},
	}

	oa := testdb.Org1.Load(t, rt)
	err := task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.IVRFlow) // event skipped Ann because she has a waiting session
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.PickANumber)
	testsuite.AssertContactInFlow(t, rt, testdb.Dan, testdb.PickANumber)

	// check we recorded recent triggers for this event
	assertvk.Keys(t, vc, "recent_campaign_fires:*", []string{"recent_campaign_fires:10002"})
	assertvk.ZRange(t, vc, "recent_campaign_fires:10002", 0, -1, []string{"QQFoOgV99A|10001", "vWOxKKbX2M|10003"})

	// create task for event #2 (single message, start mode PASSIVE)
	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint2.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Bob.ID, testdb.Ann.ID, testdb.Dan.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// everyone still in the same flows
	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.IVRFlow)
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.PickANumber)
	testsuite.AssertContactInFlow(t, rt, testdb.Dan, testdb.PickANumber)

	// and should have a queued message
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Hi Ann, it is time to consult with your patients.' AND status = 'Q'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Hi Bob, it is time to consult with your patients.' AND status = 'Q'`).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text = 'Hi Dan, it is time to consult with your patients.' AND status = 'Q'`).Returns(1)

	// check we recorded recent triggers for this event
	assertvk.Keys(t, vc, "recent_campaign_fires:*", []string{"recent_campaign_fires:10001", "recent_campaign_fires:10002"})
	assertvk.ZRange(t, vc, "recent_campaign_fires:10001", 0, -1, []string{"nU/8BkiRuI|10000", "8bPiuaeAX6|10001", "VtFTaBQT2V|10003"})
	assertvk.ZRange(t, vc, "recent_campaign_fires:10002", 0, -1, []string{"QQFoOgV99A|10001", "vWOxKKbX2M|10003"})

	// create task for event #1 (Favorites, start mode INTERRUPT)
	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint1.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Bob.ID, testdb.Ann.ID, testdb.Dan.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// everyone should be in campaign point flow
	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Dan, testdb.Favorites)

	// and their previous waiting sessions will have been interrupted
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Bob.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Ann.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Dan.UUID).Returns(1)

	// test task when campaign point has been deleted
	rt.DB.MustExec(`UPDATE campaigns_campaignevent SET is_active = FALSE WHERE id = $1`, testdb.RemindersPoint1.ID)
	models.FlushCache()
	oa = testdb.Org1.Load(t, rt)

	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint1.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Bob.ID, testdb.Ann.ID, testdb.Dan.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// task should be a noop, no new sessions created
	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Dan, testdb.Favorites)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Bob.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Ann.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Dan.UUID).Returns(1)

	// test task when flow has been deleted
	rt.DB.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, testdb.PickANumber.ID)
	models.FlushCache()
	oa = testdb.Org1.Load(t, rt)

	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint3.ID,
		ContactIDs:  []models.ContactID{testdb.Bob.ID, testdb.Ann.ID, testdb.Dan.ID},
		FireVersion: 1,
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// task should be a noop, no new sessions created
	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Dan, testdb.Favorites)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Bob.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Ann.UUID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE contact_uuid = $1 AND status = 'I'`, testdb.Dan.UUID).Returns(1)
}

func TestBulkCampaignTriggerModes(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// create waiting messaging sessions for Ann and Bob, Cat and Dan have no session
	testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Ann, models.FlowTypeMessaging, nil, testdb.Favorites)
	testdb.InsertWaitingSession(t, rt, testdb.Org1, testdb.Bob, models.FlowTypeMessaging, nil, testdb.PickANumber)

	oa := testdb.Org1.Load(t, rt)

	// 1. skip mode with flow point (#3) - contacts with messaging sessions should be skipped
	task := &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint3.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID},
	}
	err := task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.Favorites)   // skipped, still in Favorites
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.PickANumber) // skipped, still in Pick A Number
	testsuite.AssertContactInFlow(t, rt, testdb.Cat, testdb.PickANumber) // started in Pick A Number
	testsuite.AssertContactInFlow(t, rt, testdb.Dan, testdb.PickANumber) // started in Pick A Number

	// no sessions were interrupted (skip mode doesn't interrupt)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE status = 'I'`).Returns(0)

	// 2. background mode with message point (#2) - sends to all regardless of session state
	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint2.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// all 4 contacts should have received messages
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text LIKE 'Hi %, it is time to consult%'`).Returns(4)

	// contacts still in their original flows (background mode doesn't interrupt)
	testsuite.AssertContactInFlow(t, rt, testdb.Ann, testdb.Favorites)
	testsuite.AssertContactInFlow(t, rt, testdb.Bob, testdb.PickANumber)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE status = 'I'`).Returns(0)

	// 3. change point #2 to skip mode and fire - contacts with sessions should be skipped
	rt.DB.MustExec(`UPDATE campaigns_campaignevent SET start_mode = 'S' WHERE id = $1`, testdb.RemindersPoint2.ID)
	models.FlushCache()
	oa = testdb.Org1.Load(t, rt)

	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint2.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// all 4 contacts have sessions so all should be skipped - no new messages
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text LIKE 'Hi %, it is time to consult%'`).Returns(4)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE status = 'I'`).Returns(0)

	// 4. change point #2 to interrupt mode and fire - sessions interrupted, all get messages
	rt.DB.MustExec(`UPDATE campaigns_campaignevent SET start_mode = 'I' WHERE id = $1`, testdb.RemindersPoint2.ID)
	models.FlushCache()
	oa = testdb.Org1.Load(t, rt)

	task = &tasks.BulkCampaignTrigger{
		PointID:     testdb.RemindersPoint2.ID,
		FireVersion: 1,
		ContactIDs:  []models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.Cat.ID, testdb.Dan.ID},
	}
	err = task.Perform(ctx, rt, oa)
	assert.NoError(t, err)

	// all contacts should have received messages (4 from step 2 + 4 new = 8)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE text LIKE 'Hi %, it is time to consult%'`).Returns(8)

	// all previous sessions should have been interrupted
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowsession WHERE status = 'I'`).Returns(4)
}
