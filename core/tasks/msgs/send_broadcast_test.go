package msgs_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	_ "github.com/nyaruka/mailroom/core/runner/handlers"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/msgs"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcastsFromEvents(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	eng := i18n.Language("eng")
	basic := flows.BroadcastTranslations{
		eng: {
			Text:         "hello world",
			Attachments:  nil,
			QuickReplies: nil,
		},
	}

	doctors := assets.NewGroupReference(testdb.DoctorsGroup.UUID, "Doctors")
	ann := flows.NewContactReference(testdb.Ann.UUID, "Ann")

	// add an extra URN fo Ann
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, urns.URN("tel:+12065551212"), 1001, nil)

	// change Cat's URN to an invalid twitter URN so it can't be sent
	rt.DB.MustExec(`UPDATE contacts_contacturn SET identity = 'twitter:invalid-urn', scheme = 'twitter', path='invalid-urn' WHERE id = $1`, testdb.Cat.URNID)
	cat := flows.NewContactReference(testdb.Cat.UUID, "Cat")
	catOnly := []*flows.ContactReference{cat}

	tcs := []struct {
		translations       flows.BroadcastTranslations
		baseLanguage       i18n.Language
		groups             []*assets.GroupReference
		contacts           []*flows.ContactReference
		urns               []urns.URN
		queue              queues.Fair
		expectedBatchCount int
		expectedMsgCount   int
		expectedMsgText    string
	}{
		{ // 0
			translations:       basic,
			baseLanguage:       eng,
			groups:             []*assets.GroupReference{doctors},
			contacts:           nil,
			urns:               nil,
			queue:              rt.Queues.Batch,
			expectedBatchCount: 2,
			expectedMsgCount:   121,
			expectedMsgText:    "hello world",
		},
		{ // 1
			translations:       basic,
			baseLanguage:       eng,
			groups:             []*assets.GroupReference{doctors},
			contacts:           catOnly,
			urns:               nil,
			queue:              rt.Queues.Batch,
			expectedBatchCount: 2,
			expectedMsgCount:   122,
			expectedMsgText:    "hello world",
		},
		{ // 2
			translations:       basic,
			baseLanguage:       eng,
			groups:             nil,
			contacts:           catOnly,
			urns:               nil,
			queue:              rt.Queues.Realtime,
			expectedBatchCount: 1,
			expectedMsgCount:   1,
			expectedMsgText:    "hello world",
		},
		{ // 3
			translations:       basic,
			baseLanguage:       eng,
			groups:             []*assets.GroupReference{doctors},
			contacts:           []*flows.ContactReference{ann},
			urns:               nil,
			queue:              rt.Queues.Batch,
			expectedBatchCount: 2,
			expectedMsgCount:   121,
			expectedMsgText:    "hello world",
		},
		{ // 4
			translations:       basic,
			baseLanguage:       eng,
			groups:             nil,
			contacts:           []*flows.ContactReference{ann},
			urns:               nil,
			queue:              rt.Queues.Realtime,
			expectedBatchCount: 1,
			expectedMsgCount:   1,
			expectedMsgText:    "hello world",
		},
		{ // 5
			translations:       basic,
			baseLanguage:       eng,
			groups:             nil,
			contacts:           []*flows.ContactReference{ann},
			urns:               []urns.URN{urns.URN("tel:+12065551212")},
			queue:              rt.Queues.Realtime,
			expectedBatchCount: 1,
			expectedMsgCount:   1,
			expectedMsgText:    "hello world",
		},
		{ // 6
			translations:       basic,
			baseLanguage:       eng,
			groups:             nil,
			contacts:           []*flows.ContactReference{ann},
			urns:               []urns.URN{urns.URN("tel:+250700000001")},
			queue:              rt.Queues.Realtime,
			expectedBatchCount: 1,
			expectedMsgCount:   2,
			expectedMsgText:    "hello world",
		},
		{ // 7
			translations:       basic,
			baseLanguage:       eng,
			groups:             nil,
			contacts:           nil,
			urns:               []urns.URN{urns.URN("tel:+250700000001")},
			queue:              rt.Queues.Realtime,
			expectedBatchCount: 1,
			expectedMsgCount:   1,
			expectedMsgText:    "hello world",
		},
	}

	lastNow := time.Now()
	time.Sleep(10 * time.Millisecond)

	for i, tc := range tcs {
		testsuite.ReindexElastic(t, rt)

		// handle our start task
		event := events.NewBroadcastCreated(tc.translations, tc.baseLanguage, tc.groups, tc.contacts, "", tc.urns)
		bcast, err := models.NewBroadcastFromEvent(ctx, rt.DB, oa, event)
		assert.NoError(t, err)

		err = tasks.Queue(ctx, rt, tc.queue, testdb.Org1.ID, &msgs.SendBroadcastTask{Broadcast: bcast}, false)
		assert.NoError(t, err)

		taskCounts := testsuite.FlushTasks(t, rt)

		// assert our count of batches
		assert.Equal(t, tc.expectedBatchCount, taskCounts["send_broadcast_batch"], "%d: unexpected batch count", i)

		// assert our count of total msgs created
		assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE org_id = 1 AND created_on > $1 AND text = $2`, lastNow, tc.expectedMsgText).
			Returns(tc.expectedMsgCount, "%d: unexpected msg count", i)

		lastNow = time.Now()
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSendBroadcastTask(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	polls := testdb.InsertOptIn(t, rt, testdb.Org1, "Polls")

	rt.DB.MustExec(`UPDATE orgs_org SET flow_languages = '{"eng", "spa"}' WHERE id = $1`, testdb.Org1.ID)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOrg|models.RefreshOptIns)
	assert.NoError(t, err)

	// add an extra URN for Ann, change Cat's language to Spanish, and mark Bob as seen recently
	testdb.InsertContactURN(t, rt, testdb.Org1, testdb.Ann, urns.URN("tel:+12065551212"), 1001, nil)
	rt.DB.MustExec(`UPDATE contacts_contact SET language = 'spa', modified_on = NOW() WHERE id = $1`, testdb.Cat.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET last_seen_on = NOW() - interval '45 days', modified_on = NOW() WHERE id = $1`, testdb.Bob.ID)

	testsuite.ReindexElastic(t, rt)

	tcs := []struct {
		translations    flows.BroadcastTranslations
		baseLanguage    i18n.Language
		expressions     bool
		optIn           *testdb.OptIn
		groupIDs        []models.GroupID
		contactIDs      []models.ContactID
		URNs            []urns.URN
		query           string
		exclusions      models.Exclusions
		createdByID     models.UserID
		queue           queues.Fair
		expectedBatches int
		expectedMsgs    map[string]int
	}{
		{
			translations: flows.BroadcastTranslations{
				"eng": {Text: "hello world"},
			},
			baseLanguage:    "eng",
			expressions:     false,
			optIn:           polls,
			groupIDs:        []models.GroupID{testdb.DoctorsGroup.ID},
			contactIDs:      []models.ContactID{testdb.Ann.ID},
			exclusions:      models.NoExclusions,
			createdByID:     testdb.Admin.ID,
			queue:           rt.Queues.Batch,
			expectedBatches: 2,
			expectedMsgs:    map[string]int{"hello world": 121},
		},
		{
			translations: flows.BroadcastTranslations{
				"eng": {Text: "hi @(title(contact.name)) from @globals.org_name goflow URN: @urns.tel Gender: @fields.gender"},
			},
			baseLanguage:    "eng",
			expressions:     true,
			contactIDs:      []models.ContactID{testdb.Ann.ID},
			exclusions:      models.NoExclusions,
			createdByID:     testdb.Agent.ID,
			queue:           rt.Queues.Realtime,
			expectedBatches: 1,
			expectedMsgs:    map[string]int{"hi Ann from TextIt goflow URN: tel:+12065551212 Gender: F": 1},
		},
		{
			translations: flows.BroadcastTranslations{
				"eng": {Text: "hello"},
				"spa": {Text: "hola"},
			},
			baseLanguage:    "eng",
			expressions:     true,
			query:           "name = Ann OR name = Cat OR name = Bob",
			exclusions:      models.NoExclusions,
			queue:           rt.Queues.Batch,
			expectedBatches: 1,
			expectedMsgs:    map[string]int{"hello": 2, "hola": 1},
		},
		{
			translations: flows.BroadcastTranslations{
				"eng": {Text: "goodbye"},
				"spa": {Text: "chau"},
			},
			baseLanguage:    "eng",
			expressions:     true,
			query:           "name = Ann OR name = Cat OR name = Bob",
			exclusions:      models.Exclusions{NotSeenSinceDays: 60},
			queue:           rt.Queues.Batch,
			expectedBatches: 1,
			expectedMsgs:    map[string]int{"goodbye": 1},
		},
	}

	lastNow := time.Now()
	time.Sleep(10 * time.Millisecond)

	for i, tc := range tcs {
		var optInID models.OptInID
		if tc.optIn != nil {
			optInID = tc.optIn.ID
		}

		bcast := models.NewBroadcast(oa.OrgID(), tc.translations, tc.baseLanguage, tc.expressions, optInID, tc.groupIDs, tc.contactIDs, tc.URNs, tc.query, tc.exclusions, tc.createdByID)
		err := models.InsertBroadcast(ctx, rt.DB, bcast)
		assert.NoError(t, err)

		task := &msgs.SendBroadcastTask{Broadcast: bcast}

		err = tasks.Queue(ctx, rt, rt.Queues.Batch, testdb.Org1.ID, task, false)
		assert.NoError(t, err)

		taskCounts := testsuite.FlushTasks(t, rt)

		// assert our count of batches
		assert.Equal(t, tc.expectedBatches, taskCounts["send_broadcast_batch"], "%d: unexpected batch count", i)

		// assert our count of msgs created
		actualMsgs := make(map[string]int)
		rows, err := rt.DB.QueryContext(ctx, `SELECT text, count(*) FROM msgs_msg WHERE org_id = 1 AND created_on > $1 GROUP BY text`, lastNow)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var text string
			var count int
			require.NoError(t, rows.Scan(&text, &count))
			actualMsgs[text] = count
		}

		assert.Equal(t, tc.expectedMsgs, actualMsgs, "%d: msg count mismatch", i)

		if tc.optIn != nil {
			assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE org_id = 1 AND created_on > $1 AND optin_id = $2`, lastNow, optInID)
		}

		lastNow = time.Now()
		time.Sleep(5 * time.Millisecond)
	}
}
