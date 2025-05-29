package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdata"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcasts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	optIn := testdata.InsertOptIn(rt, testdata.Org1, "Polls")

	bcast := models.NewBroadcast(
		testdata.Org1.ID,
		flows.BroadcastTranslations{"eng": {Text: "Hi there"}},
		"eng",
		true,
		optIn.ID,
		[]models.GroupID{testdata.DoctorsGroup.ID},
		[]models.ContactID{testdata.Alexandra.ID, testdata.Bob.ID, testdata.Cathy.ID},
		[]urns.URN{"tel:+593979012345"},
		"age > 33",
		models.NoExclusions,
		models.NilUserID,
	)
	bcast.TemplateID = testdata.GoodbyeTemplate.ID
	bcast.TemplateVariables = []string{"@contact.name"}

	err := models.InsertBroadcast(ctx, rt.DB, bcast)
	assert.NoError(t, err)
	assert.NotEqual(t, models.NilBroadcastID, bcast.ID)

	assertdb.Query(t, rt.DB, `SELECT base_language, translations->'eng'->>'text' AS text, template_id, template_variables[1] as var1, query FROM msgs_broadcast WHERE id = $1`, bcast.ID).Columns(map[string]any{
		"base_language": "eng", "text": "Hi there", "query": "age > 33", "template_id": int64(testdata.GoodbyeTemplate.ID), "var1": "@contact.name",
	})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_broadcast_groups WHERE broadcast_id = $1`, bcast.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_broadcast_contacts WHERE broadcast_id = $1`, bcast.ID).Returns(3)

	err = bcast.SetQueued(ctx, rt.DB, 5)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status, contact_count FROM msgs_broadcast WHERE id = $1`, bcast.ID).Columns(map[string]any{"status": "Q", "contact_count": int64(5)})

	err = bcast.SetStarted(ctx, rt.DB)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status FROM msgs_broadcast WHERE id = $1`, bcast.ID).Returns("S")

	err = bcast.SetCompleted(ctx, rt.DB)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status FROM msgs_broadcast WHERE id = $1`, bcast.ID).Returns("C")

	err = bcast.SetFailed(ctx, rt.DB)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status FROM msgs_broadcast WHERE id = $1`, bcast.ID).Returns("F")
}

func TestInsertChildBroadcast(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	optIn := testdata.InsertOptIn(rt, testdata.Org1, "Polls")
	schedID := testdata.InsertSchedule(rt, testdata.Org1, models.RepeatPeriodDaily, time.Now())
	bcastID := testdata.InsertBroadcast(rt, testdata.Org1, `eng`, map[i18n.Language]string{`eng`: "Hello"}, optIn, schedID, []*testdata.Contact{testdata.Bob, testdata.Cathy}, nil)

	var bj json.RawMessage
	err := rt.DB.GetContext(ctx, &bj, `SELECT ROW_TO_JSON(r) FROM (
		SELECT id, org_id, translations, base_language, optin_id, template_id, template_variables, query, created_by_id, parent_id FROM msgs_broadcast WHERE id = $1
	) r`, bcastID)
	require.NoError(t, err)

	parent := &models.Broadcast{}
	jsonx.MustUnmarshal(bj, parent)

	child, err := models.InsertChildBroadcast(ctx, rt.DB, parent)
	assert.NoError(t, err)
	assert.Equal(t, parent.ID, child.ParentID)
	assert.Equal(t, parent.OrgID, child.OrgID)
	assert.Equal(t, parent.BaseLanguage, child.BaseLanguage)
	assert.Equal(t, parent.OptInID, child.OptInID)
	assert.Equal(t, parent.TemplateID, child.TemplateID)
	assert.Equal(t, parent.TemplateVariables, child.TemplateVariables)
}

func TestNonPersistentBroadcasts(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	translations := flows.BroadcastTranslations{"eng": {Text: "Hi there"}}
	optIn := testdata.InsertOptIn(rt, testdata.Org1, "Polls")

	// create a broadcast which doesn't actually exist in the DB
	bcast := models.NewBroadcast(
		testdata.Org1.ID,
		translations,
		"eng",
		true,
		optIn.ID,
		[]models.GroupID{testdata.DoctorsGroup.ID},
		[]models.ContactID{testdata.Alexandra.ID, testdata.Bob.ID, testdata.Cathy.ID},
		[]urns.URN{"tel:+593979012345"},
		"",
		models.NoExclusions,
		models.NilUserID,
	)

	assert.Equal(t, models.NilBroadcastID, bcast.ID)
	assert.Equal(t, testdata.Org1.ID, bcast.OrgID)
	assert.Equal(t, i18n.Language("eng"), bcast.BaseLanguage)
	assert.Equal(t, translations, bcast.Translations)
	assert.Equal(t, optIn.ID, bcast.OptInID)
	assert.Equal(t, []models.GroupID{testdata.DoctorsGroup.ID}, bcast.GroupIDs)
	assert.Equal(t, []models.ContactID{testdata.Alexandra.ID, testdata.Bob.ID, testdata.Cathy.ID}, bcast.ContactIDs)
	assert.Equal(t, []urns.URN{"tel:+593979012345"}, bcast.URNs)
	assert.Equal(t, "", bcast.Query)
	assert.Equal(t, models.NoExclusions, bcast.Exclusions)

	batch := bcast.CreateBatch([]models.ContactID{testdata.Alexandra.ID, testdata.Bob.ID}, true, false)

	assert.Equal(t, models.NilBroadcastID, batch.BroadcastID)
	assert.NotNil(t, testdata.Org1.ID, batch.Broadcast)
	assert.Equal(t, []models.ContactID{testdata.Alexandra.ID, testdata.Bob.ID}, batch.ContactIDs)

	oa, err := models.GetOrgAssets(ctx, rt, testdata.Org1.ID)
	require.NoError(t, err)

	msgs, err := bcast.CreateMessages(ctx, rt, oa, batch)
	require.NoError(t, err)

	assert.Equal(t, 2, len(msgs))

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_msg WHERE direction = 'O' AND broadcast_id IS NULL AND text = 'Hi there'`).Returns(2)
}

func TestBroadcastBatchCreateMessage(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetRedis)

	polls := testdata.InsertOptIn(rt, testdata.Org1, "Polls")

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdata.Org1.ID, models.RefreshOptIns)
	require.NoError(t, err)

	tcs := []struct {
		contactLanguage      i18n.Language
		contactURN           urns.URN
		translations         flows.BroadcastTranslations
		baseLanguage         i18n.Language
		expressions          bool
		optInID              models.OptInID
		templateID           models.TemplateID
		templateVariables    []string
		expectedText         string
		expectedAttachments  []utils.Attachment
		expectedQuickReplies []flows.QuickReply
		expectedLocale       i18n.Locale
		expectedError        string
	}{
		{ // 0
			contactURN:           "tel:+593979000000",
			contactLanguage:      i18n.NilLanguage,
			translations:         flows.BroadcastTranslations{"eng": {Text: "Hi @contact"}},
			baseLanguage:         "eng",
			expressions:          false,
			expectedText:         "Hi @contact",
			expectedAttachments:  []utils.Attachment{},
			expectedQuickReplies: []flows.QuickReply{},
			expectedLocale:       "eng-EC",
		},
		{ // 1: contact language not set, uses base language
			contactURN:           "tel:+593979000001",
			contactLanguage:      i18n.NilLanguage,
			translations:         flows.BroadcastTranslations{"eng": {Text: "Hello @contact.name"}, "spa": {Text: "Hola @contact.name"}},
			baseLanguage:         "eng",
			expressions:          true,
			expectedText:         "Hello Felix",
			expectedAttachments:  []utils.Attachment{},
			expectedQuickReplies: []flows.QuickReply{},
			expectedLocale:       "eng-EC",
		},
		{ // 2: contact language iggnored if it isn't a valid org language, even if translation exists
			contactURN:           "tel:+593979000002",
			contactLanguage:      "spa",
			translations:         flows.BroadcastTranslations{"eng": {Text: "Hello @contact.name"}, "spa": {Text: "Hola @contact.name"}},
			baseLanguage:         "eng",
			expressions:          true,
			expectedText:         "Hello Felix",
			expectedAttachments:  []utils.Attachment{},
			expectedQuickReplies: []flows.QuickReply{},
			expectedLocale:       "eng-EC",
		},
		{ // 3: contact language used
			contactURN:      "tel:+593979000003",
			contactLanguage: "fra",
			translations: flows.BroadcastTranslations{
				"eng": {Text: "Hello @contact.name", Attachments: []utils.Attachment{"audio/mp3:http://test.en.mp3"}, QuickReplies: []flows.QuickReply{{Text: "yes"}, {Text: "no"}}},
				"fra": {Text: "Bonjour @contact.name", Attachments: []utils.Attachment{"audio/mp3:http://test.fr.mp3"}, QuickReplies: []flows.QuickReply{{Text: "oui"}, {Text: "no"}}},
			},
			baseLanguage:         "eng",
			expressions:          true,
			expectedText:         "Bonjour Felix",
			expectedAttachments:  []utils.Attachment{"audio/mp3:http://test.fr.mp3"},
			expectedQuickReplies: []flows.QuickReply{{Text: "oui"}, {Text: "no"}},
			expectedLocale:       "fra-EC",
		},
		{ // 5: broadcast with optin
			contactURN:           "facebook:1000000000001",
			contactLanguage:      i18n.NilLanguage,
			translations:         flows.BroadcastTranslations{"eng": {Text: "Hi @contact"}},
			baseLanguage:         "eng",
			expressions:          true,
			optInID:              polls.ID,
			expectedText:         "Hi Felix",
			expectedAttachments:  []utils.Attachment{},
			expectedQuickReplies: []flows.QuickReply{},
			expectedLocale:       "eng",
		},
		{ // 6: broadcast with template
			contactURN:           "facebook:1000000000002",
			contactLanguage:      "eng",
			translations:         flows.BroadcastTranslations{"eng": {Text: "Hi @contact"}},
			baseLanguage:         "eng",
			expressions:          true,
			templateID:           testdata.ReviveTemplate.ID,
			templateVariables:    []string{"@contact.name", "mice"},
			expectedText:         "Hi Felix, are you still experiencing problems with mice?",
			expectedAttachments:  []utils.Attachment{},
			expectedQuickReplies: []flows.QuickReply{},
			expectedLocale:       "eng-US",
		},
	}

	for i, tc := range tcs {
		contact := testdata.InsertContact(rt, testdata.Org1, flows.NewContactUUID(), "Felix", tc.contactLanguage, models.ContactStatusActive)
		testdata.InsertContactURN(rt, testdata.Org1, contact, tc.contactURN, 1000, nil)

		bcast := &models.Broadcast{
			OrgID:             testdata.Org1.ID,
			Translations:      tc.translations,
			BaseLanguage:      tc.baseLanguage,
			Expressions:       tc.expressions,
			OptInID:           tc.optInID,
			TemplateID:        tc.templateID,
			TemplateVariables: tc.templateVariables,
		}
		err = models.InsertBroadcast(ctx, rt.DB, bcast)
		require.NoError(t, err)

		batch := &models.BroadcastBatch{
			BroadcastID: bcast.ID,
			Broadcast:   bcast,
			ContactIDs:  []models.ContactID{contact.ID},
			IsLast:      true,
		}

		msgs, err := bcast.CreateMessages(ctx, rt, oa, batch)
		if tc.expectedError != "" {
			assert.EqualError(t, err, tc.expectedError, "error mismatch in test case %d", i)
		} else {
			assert.NoError(t, err, "unexpected error in test case %d", i)
			if assert.Len(t, msgs, 1, "msg count mismatch in test case %d", i) {
				assert.Equal(t, tc.expectedText, msgs[0].Text(), "%d: msg text mismatch", i)
				assert.Equal(t, tc.expectedAttachments, msgs[0].Attachments(), "%d: attachments mismatch", i)
				assert.Equal(t, tc.expectedQuickReplies, msgs[0].QuickReplies(), "%d: quick replies mismatch", i)
				assert.Equal(t, tc.expectedLocale, msgs[0].Locale(), "%d: msg locale mismatch", i)
				assert.Equal(t, tc.optInID, msgs[0].OptInID(), "%d: optin id mismatch", i)
			}
		}
	}
}
