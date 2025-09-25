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
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBroadcasts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	optIn := testdb.InsertOptIn(t, rt, testdb.Org1, "45aec4dd-945f-4511-878f-7d8516fbd336", "Polls")

	bcast := models.NewBroadcast(
		testdb.Org1.ID,
		flows.BroadcastTranslations{"eng": {Text: "Hi there"}},
		"eng",
		true,
		optIn.ID,
		[]models.GroupID{testdb.DoctorsGroup.ID},
		[]models.ContactID{testdb.Dan.ID, testdb.Bob.ID, testdb.Ann.ID},
		[]urns.URN{"tel:+593979012345"},
		"age > 33",
		models.NoExclusions,
		models.NilUserID,
	)
	bcast.TemplateID = testdb.GoodbyeTemplate.ID
	bcast.TemplateVariables = []string{"@contact.name"}

	err := models.InsertBroadcast(ctx, rt.DB, bcast)
	assert.NoError(t, err)
	assert.NotEqual(t, models.NilBroadcastID, bcast.ID)

	assertdb.Query(t, rt.DB, `SELECT base_language, translations->'eng'->>'text' AS text, template_id, template_variables[1] as var1, query FROM msgs_broadcast WHERE id = $1`, bcast.ID).Columns(map[string]any{
		"base_language": "eng", "text": "Hi there", "query": "age > 33", "template_id": int64(testdb.GoodbyeTemplate.ID), "var1": "@contact.name",
	})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_broadcast_groups WHERE broadcast_id = $1`, bcast.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM msgs_broadcast_contacts WHERE broadcast_id = $1`, bcast.ID).Returns(3)

	err = bcast.SetQueued(ctx, rt.DB, 5)
	assert.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status, contact_count FROM msgs_broadcast WHERE id = $1`, bcast.ID).Columns(map[string]any{"status": "Q", "contact_count": 5})

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
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	optIn := testdb.InsertOptIn(t, rt, testdb.Org1, "45aec4dd-945f-4511-878f-7d8516fbd336", "Polls")
	schedID := testdb.InsertSchedule(t, rt, testdb.Org1, models.RepeatPeriodDaily, time.Now())
	bcast := testdb.InsertBroadcast(t, rt, testdb.Org1, `eng`, map[i18n.Language]string{`eng`: "Hello"}, optIn, schedID, []*testdb.Contact{testdb.Bob, testdb.Ann}, nil)

	var bj json.RawMessage
	err := rt.DB.GetContext(ctx, &bj, `SELECT ROW_TO_JSON(r) FROM (
		SELECT id, org_id, translations, base_language, optin_id, template_id, template_variables, query, created_by_id, parent_id FROM msgs_broadcast WHERE id = $1
	) r`, bcast.ID)
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
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	translations := flows.BroadcastTranslations{"eng": {Text: "Hi there"}}
	optIn := testdb.InsertOptIn(t, rt, testdb.Org1, "45aec4dd-945f-4511-878f-7d8516fbd336", "Polls")

	// create a broadcast which doesn't actually exist in the DB
	bcast := models.NewBroadcast(
		testdb.Org1.ID,
		translations,
		"eng",
		true,
		optIn.ID,
		[]models.GroupID{testdb.DoctorsGroup.ID},
		[]models.ContactID{testdb.Dan.ID, testdb.Bob.ID, testdb.Ann.ID},
		[]urns.URN{"tel:+593979012345"},
		"",
		models.NoExclusions,
		models.NilUserID,
	)

	assert.Equal(t, models.NilBroadcastID, bcast.ID)
	assert.Equal(t, testdb.Org1.ID, bcast.OrgID)
	assert.Equal(t, i18n.Language("eng"), bcast.BaseLanguage)
	assert.Equal(t, translations, bcast.Translations)
	assert.Equal(t, optIn.ID, bcast.OptInID)
	assert.Equal(t, []models.GroupID{testdb.DoctorsGroup.ID}, bcast.GroupIDs)
	assert.Equal(t, []models.ContactID{testdb.Dan.ID, testdb.Bob.ID, testdb.Ann.ID}, bcast.ContactIDs)
	assert.Equal(t, []urns.URN{"tel:+593979012345"}, bcast.URNs)
	assert.Equal(t, "", bcast.Query)
	assert.Equal(t, models.NoExclusions, bcast.Exclusions)

	batch := bcast.CreateBatch([]models.ContactID{testdb.Dan.ID, testdb.Bob.ID}, true, false)

	assert.Equal(t, models.NilBroadcastID, batch.BroadcastID)
	assert.NotNil(t, testdb.Org1.ID, batch.Broadcast)
	assert.Equal(t, []models.ContactID{testdb.Dan.ID, testdb.Bob.ID}, batch.ContactIDs)
}

func TestBroadcastSend(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshOptIns)
	require.NoError(t, err)

	test.MockUniverse()

	tcs := []struct {
		contactLanguage   i18n.Language
		contactURN        urns.URN
		translations      flows.BroadcastTranslations
		baseLanguage      i18n.Language
		expressions       bool
		optInID           models.OptInID
		templateID        models.TemplateID
		templateVariables []string
		expected          []byte
	}{
		{ // 0
			contactURN:      "tel:+593979000000",
			contactLanguage: i18n.NilLanguage,
			translations:    flows.BroadcastTranslations{"eng": {Text: "Hi @contact"}},
			baseLanguage:    "eng",
			expressions:     false,
			expected: []byte(`{
				"uuid": "01969b47-096b-76f8-9c0b-2014ddc77094",
				"type": "msg_created",
				"created_on": "2025-05-04T12:30:47.123456789Z",
				"msg": {
					"urn": "tel:+593979000000?id=30000",
					"channel": {
						"uuid": "8e7b62ee-2e84-4601-8fef-2e44c490b43e",
						"name": "Android"
					},
					"text": "Hi @contact",
					"locale": "eng-EC"
				},
				"broadcast_uuid": "01969b47-0583-76f8-bd38-d266ec8d3716"
			}`),
		},
		{ // 1: contact language not set, uses base language
			contactURN:      "tel:+593979000001",
			contactLanguage: i18n.NilLanguage,
			translations:    flows.BroadcastTranslations{"eng": {Text: "Hello @contact.name"}, "spa": {Text: "Hola @contact.name"}},
			baseLanguage:    "eng",
			expressions:     true,
			expected: []byte(`{
				"uuid": "01969b47-1523-76f8-8f41-6b2d9f33d623",
				"type": "msg_created",
				"created_on": "2025-05-04T12:30:50.123456789Z",
				"msg": {
					"urn": "tel:+593979000001?id=30001",
					"channel": {
						"uuid": "8e7b62ee-2e84-4601-8fef-2e44c490b43e",
						"name": "Android"
					},
					"text": "Hello Felix",
					"locale": "eng-EC"
				},
				"broadcast_uuid": "01969b47-113b-76f8-8228-9728778b6c98"
			}`),
		},
		{ // 2: contact language iggnored if it isn't a valid org language, even if translation exists
			contactURN:      "tel:+593979000002",
			contactLanguage: "spa",
			translations:    flows.BroadcastTranslations{"eng": {Text: "Hello @contact.name"}, "spa": {Text: "Hola @contact.name"}},
			baseLanguage:    "eng",
			expressions:     true,
			expected: []byte(`{
				"uuid": "01969b47-20db-76f8-b86e-4b881f09a186",
				"type": "msg_created",
				"created_on": "2025-05-04T12:30:53.123456789Z",
				"msg": {
					"urn": "tel:+593979000002?id=30002",
					"channel": {
						"uuid": "8e7b62ee-2e84-4601-8fef-2e44c490b43e",
						"name": "Android"
					},
					"text": "Hello Felix",
					"locale": "eng-EC"
				},
				"broadcast_uuid": "01969b47-1cf3-76f8-ba00-bd7f0d08e671"
			}`),
		},
		{ // 3: contact language used
			contactURN:      "tel:+593979000003",
			contactLanguage: "fra",
			translations: flows.BroadcastTranslations{
				"eng": {Text: "Hello @contact.name", Attachments: []utils.Attachment{"audio/mp3:http://test.en.mp3"}, QuickReplies: []flows.QuickReply{{Text: "yes"}, {Text: "no"}}},
				"fra": {Text: "Bonjour @contact.name", Attachments: []utils.Attachment{"audio/mp3:http://test.fr.mp3"}, QuickReplies: []flows.QuickReply{{Text: "oui"}, {Text: "no"}}},
			},
			baseLanguage: "eng",
			expressions:  true,
			expected: []byte(`{
				"uuid": "01969b47-2c93-76f8-8dbf-00ecf5d03034",
				"type": "msg_created",
				"created_on": "2025-05-04T12:30:56.123456789Z",
				"msg": {
					"urn": "tel:+593979000003?id=30003",
					"channel": {
						"uuid": "8e7b62ee-2e84-4601-8fef-2e44c490b43e",
						"name": "Android"
					},
					"text": "Bonjour Felix",
					"attachments": [
						"audio/mp3:http://test.fr.mp3"
					],
					"quick_replies": [
						{
							"text": "oui"
						},
						{
							"text": "no"
						}
					],
					"locale": "fra-EC"
				},
				"broadcast_uuid": "01969b47-28ab-76f8-bebe-b4a1f677cf4c"
			}`),
		},
		{ // 4: broadcast with template
			contactURN:        "facebook:1000000000002",
			contactLanguage:   "eng",
			translations:      flows.BroadcastTranslations{"eng": {Text: "Hi @contact"}},
			baseLanguage:      "eng",
			expressions:       true,
			templateID:        testdb.ReviveTemplate.ID,
			templateVariables: []string{"@contact.name", "mice"},
			expected: []byte(`{
				"uuid": "01969b47-384b-76f8-9654-8a7258fbaae4",
				"type": "msg_created",
				"created_on": "2025-05-04T12:30:59.123456789Z",
				"msg": {
					"urn": "facebook:1000000000002?id=30004",
					"channel": {
						"uuid": "0f661e8b-ea9d-4bd3-9953-d368340acf91",
						"name": "Facebook"
					},
					"text": "Hi Felix, are you still experiencing problems with mice?",
					"templating": {
						"template": {
							"uuid": "9c22b594-fcab-4b29-9bcb-ce4404894a80",
							"name": "revive_issue"
						},
						"components": [
							{
								"name": "body",
								"type": "body/text",
								"variables": {
									"1": 0,
									"2": 1
								}
							}
						],
						"variables": [
							{
								"type": "text",
								"value": "Felix"
							},
							{
								"type": "text",
								"value": "mice"
							}
						]
					},
					"locale": "eng-US"
				},
				"broadcast_uuid": "01969b47-3463-76f8-afcb-91a2073e5459"
			}`),
		},
	}

	for i, tc := range tcs {
		contact := testdb.InsertContact(t, rt, testdb.Org1, flows.NewContactUUID(), "Felix", tc.contactLanguage, models.ContactStatusActive)
		testdb.InsertContactURN(t, rt, testdb.Org1, contact, tc.contactURN, 1000, nil)

		bcast := &models.Broadcast{
			UUID:              flows.NewBroadcastUUID(),
			OrgID:             testdb.Org1.ID,
			Translations:      tc.translations,
			BaseLanguage:      tc.baseLanguage,
			Expressions:       tc.expressions,
			OptInID:           tc.optInID,
			TemplateID:        tc.templateID,
			TemplateVariables: tc.templateVariables,
		}
		err = models.InsertBroadcast(ctx, rt.DB, bcast)
		require.NoError(t, err)

		_, ec, _ := contact.Load(t, rt, oa)

		evt := bcast.Send(rt, oa, ec)

		assert.JSONEq(t, string(tc.expected), string(jsonx.MustMarshal(evt)), "%d: msg json mismatch", i)
	}
}
