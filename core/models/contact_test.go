package models_test

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/test"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContacts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	// for now it's still possible to have more than one open ticket in the database
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-717a-a39e-e8ca91fb7262", testdb.Org1, testdb.Ann, testdb.SupportTopic, time.Now(), testdb.Agent)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-725e-be9c-0c6407efd755", testdb.Org1, testdb.Ann, testdb.SalesTopic, time.Now(), nil)

	testdb.InsertContactURN(rt, testdb.Org1, testdb.Bob, "whatsapp:250788373373", 999, nil)
	testdb.InsertOpenTicket(rt, "01992f54-5ab6-7498-a7f2-6aa246e45cfe", testdb.Org1, testdb.Bob, testdb.DefaultTopic, time.Now(), testdb.Editor)

	org, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshAll)
	assert.NoError(t, err)

	rt.DB.MustExec(`DELETE FROM contacts_contacturn WHERE contact_id = $1`, testdb.George.ID)
	rt.DB.MustExec(`DELETE FROM contacts_contactgroup_contacts WHERE contact_id = $1`, testdb.George.ID)
	rt.DB.MustExec(`UPDATE contacts_contact SET is_active = FALSE WHERE id = $1`, testdb.Alexandra.ID)

	mcs, err := models.LoadContacts(ctx, rt.DB, org, []models.ContactID{testdb.Ann.ID, testdb.Bob.ID, testdb.George.ID, testdb.Alexandra.ID})
	require.NoError(t, err)
	require.Equal(t, 3, len(mcs))

	// LoadContacts doesn't guarantee returned order of contacts
	sort.Slice(mcs, func(i, j int) bool { return mcs[i].ID() < mcs[j].ID() })

	// convert to goflow contacts
	contacts := make([]*flows.Contact, len(mcs))
	for i := range mcs {
		contacts[i], err = mcs[i].EngineContact(org)
		assert.NoError(t, err)
	}

	ann, bob, george := contacts[0], contacts[1], contacts[2]

	assert.Equal(t, "Ann", ann.Name())
	assert.Equal(t, len(ann.URNs()), 1)
	assert.Equal(t, ann.URNs()[0].String(), "tel:+16055741111?id=10000")
	assert.Equal(t, 1, ann.Groups().Count())
	assert.Equal(t, 2, ann.Tickets().Open().Count())

	annTicket := ann.Tickets().LastOpen()
	assert.Equal(t, "Sales", annTicket.Topic().Name())
	assert.Nil(t, annTicket.Assignee())

	assert.Equal(t, "Yobe", ann.Fields()["state"].QueryValue())
	assert.Equal(t, "Dokshi", ann.Fields()["ward"].QueryValue())
	assert.Equal(t, "F", ann.Fields()["gender"].QueryValue())
	assert.Equal(t, (*flows.FieldValue)(nil), ann.Fields()["age"])

	assert.Equal(t, "Bob", bob.Name())
	assert.NotNil(t, bob.Fields()["joined"].QueryValue())
	assert.Equal(t, 2, len(bob.URNs()))
	assert.Equal(t, "tel:+16055742222?id=10001", bob.URNs()[0].String())
	assert.Equal(t, "whatsapp:250788373373?id=30000", bob.URNs()[1].String())
	assert.Equal(t, 0, bob.Groups().Count())
	assert.NotNil(t, bob.Tickets().LastOpen())

	assert.Equal(t, "George", george.Name())
	assert.Equal(t, decimal.RequireFromString("30"), george.Fields()["age"].QueryValue())
	assert.Equal(t, 0, len(george.URNs()))
	assert.Equal(t, 0, george.Groups().Count())
	assert.Nil(t, george.Tickets().LastOpen())

	// change bob to have a preferred URN and channel of our telephone
	channel := org.ChannelByID(testdb.TwilioChannel.ID)
	err = mcs[1].UpdatePreferredURN(ctx, rt.DB, org, testdb.Bob.URNID, channel)
	assert.NoError(t, err)

	bob, err = mcs[1].EngineContact(org)
	assert.NoError(t, err)
	assert.Equal(t, "tel:+16055742222?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=10001", bob.URNs()[0].String())
	assert.Equal(t, "whatsapp:250788373373?id=30000", bob.URNs()[1].String())

	// add another tel urn to bob
	testdb.InsertContactURN(rt, testdb.Org1, testdb.Bob, urns.URN("tel:+250788373373"), 10, nil)

	// reload the contact
	mcs, err = models.LoadContacts(ctx, rt.DB, org, []models.ContactID{testdb.Bob.ID})
	assert.NoError(t, err)

	// set our preferred channel again
	err = mcs[0].UpdatePreferredURN(ctx, rt.DB, org, models.URNID(30001), channel)
	assert.NoError(t, err)

	bob, err = mcs[0].EngineContact(org)
	assert.NoError(t, err)
	assert.Equal(t, "tel:+250788373373?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30001", bob.URNs()[0].String())
	assert.Equal(t, "tel:+16055742222?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=10001", bob.URNs()[1].String())
	assert.Equal(t, "whatsapp:250788373373?id=30000", bob.URNs()[2].String())

	// no op this time
	err = mcs[0].UpdatePreferredURN(ctx, rt.DB, org, models.URNID(30001), channel)
	assert.NoError(t, err)

	bob, err = mcs[0].EngineContact(org)
	assert.NoError(t, err)
	assert.Equal(t, "tel:+250788373373?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30001", bob.URNs()[0].String())
	assert.Equal(t, "tel:+16055742222?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=10001", bob.URNs()[1].String())
	assert.Equal(t, "whatsapp:250788373373?id=30000", bob.URNs()[2].String())

	// calling with no channel is a noop on the channel
	err = mcs[0].UpdatePreferredURN(ctx, rt.DB, org, models.URNID(30001), nil)
	assert.NoError(t, err)

	bob, err = mcs[0].EngineContact(org)
	assert.NoError(t, err)
	assert.Equal(t, "tel:+250788373373?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30001", bob.URNs()[0].String())
	assert.Equal(t, "tel:+16055742222?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=10001", bob.URNs()[1].String())
	assert.Equal(t, "whatsapp:250788373373?id=30000", bob.URNs()[2].String())
}

func TestCreateContact(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertContactGroup(rt, testdb.Org1, "d636c966-79c1-4417-9f1c-82ad629773a2", "Kinyarwanda", "language = kin")

	// add an orphaned URN
	testdb.InsertContactURN(rt, testdb.Org1, nil, urns.URN("telegram:200002"), 100, nil)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	contact, flowContact, err := models.CreateContact(ctx, rt.DB, oa, models.UserID(1), "Rich", `kin`, models.ContactStatusActive, []urns.URN{urns.URN("telegram:200001"), urns.URN("telegram:200002")})
	assert.NoError(t, err)

	assert.Equal(t, "Rich", contact.Name())
	assert.Equal(t, i18n.Language(`kin`), contact.Language())
	assert.Equal(t, models.ContactStatusActive, contact.Status())
	assert.Equal(t, []urns.URN{"telegram:200001?id=30001", "telegram:200002?id=30000"}, contact.URNs())

	assert.Equal(t, "Rich", flowContact.Name())
	assert.Equal(t, i18n.Language(`kin`), flowContact.Language())
	assert.Equal(t, flows.ContactStatusActive, flowContact.Status())
	assert.Equal(t, []urns.URN{"telegram:200001?id=30001", "telegram:200002?id=30000"}, flowContact.URNs().RawURNs())
	assert.Len(t, flowContact.Groups().All(), 1)
	assert.Equal(t, assets.GroupUUID("d636c966-79c1-4417-9f1c-82ad629773a2"), flowContact.Groups().All()[0].UUID())

	_, _, err = models.CreateContact(ctx, rt.DB, oa, models.UserID(1), "Rich", `kin`, models.ContactStatusActive, []urns.URN{urns.URN("telegram:200001")})
	assert.EqualError(t, err, "URN 0 in use by other contacts")

	var uerr *models.URNError
	if assert.ErrorAs(t, err, &uerr) {
		assert.Equal(t, "taken", uerr.Code)
		assert.Equal(t, 0, uerr.Index)
	}

	// new blocked contact won't be added to smart groups
	contact, flowContact, err = models.CreateContact(ctx, rt.DB, oa, models.UserID(1), "Bob", `kin`, models.ContactStatusBlocked, []urns.URN{urns.URN("telegram:200003")})
	assert.NoError(t, err)
	assert.Equal(t, models.ContactStatusBlocked, contact.Status())
	assert.Equal(t, flows.ContactStatusBlocked, flowContact.Status())
	assert.Len(t, flowContact.Groups().All(), 0)
}

func TestCreateContactRace(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	mdb := testsuite.NewMockDB(rt.DB, func(funcName string, call int) error {
		// Make beginning a transaction take a while to create race condition. All threads will fetch
		// URN owners and decide nobody owns the URN, so all threads will try to create a new contact.
		if funcName == "BeginTxx" {
			time.Sleep(100 * time.Millisecond)
		}
		return nil
	})

	var contacts [2]*models.Contact
	var errs [2]error

	test.RunConcurrently(2, func(i int) {
		contacts[i], _, errs[i] = models.CreateContact(ctx, mdb, oa, models.UserID(1), "", i18n.NilLanguage, models.ContactStatusActive, []urns.URN{urns.URN("telegram:100007")})
	})

	// one should return a contact, the other should error
	require.True(t, (errs[0] != nil && errs[1] == nil) || (errs[0] == nil && errs[1] != nil))
	require.True(t, (contacts[0] != nil && contacts[1] == nil) || (contacts[0] == nil && contacts[1] != nil))
}

func TestGetOrCreateContact(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertContactGroup(rt, testdb.Org1, "dcc16d85-8274-4d19-a3c2-152d4ee99380", "Telegrammer", `telegram = 100001`)

	// add some orphaned URNs
	testdb.InsertContactURN(rt, testdb.Org1, nil, urns.URN("telegram:200001"), 100, nil)
	testdb.InsertContactURN(rt, testdb.Org1, nil, urns.URN("telegram:200002"), 100, nil)

	contactIDSeq := models.ContactID(30000)
	newContact := func() models.ContactID { id := contactIDSeq; contactIDSeq++; return id }
	prevContact := func() models.ContactID { return contactIDSeq - 1 }

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	tcs := []struct {
		OrgID       models.OrgID
		URNs        []urns.URN
		ContactID   models.ContactID
		Created     bool
		ContactURNs []urns.URN
		ChannelID   models.ChannelID
		GroupsUUIDs []assets.GroupUUID
	}{
		{
			testdb.Org1.ID,
			[]urns.URN{testdb.Ann.URN},
			testdb.Ann.ID,
			false,
			[]urns.URN{"tel:+16055741111?id=10000"},
			models.NilChannelID,
			[]assets.GroupUUID{testdb.DoctorsGroup.UUID},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN(testdb.Ann.URN.String() + "?foo=bar")},
			testdb.Ann.ID, // only URN identity is considered
			false,
			[]urns.URN{"tel:+16055741111?id=10000"},
			models.NilChannelID,
			[]assets.GroupUUID{testdb.DoctorsGroup.UUID},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:100001")},
			newContact(), // creates new contact
			true,
			[]urns.URN{"telegram:100001?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30002"},
			testdb.TwilioChannel.ID,
			[]assets.GroupUUID{"dcc16d85-8274-4d19-a3c2-152d4ee99380"},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:100001")},
			prevContact(), // returns the same created contact
			false,
			[]urns.URN{"telegram:100001?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30002"},
			models.NilChannelID,
			[]assets.GroupUUID{"dcc16d85-8274-4d19-a3c2-152d4ee99380"},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:100001"), urns.URN("telegram:100002")},
			prevContact(), // same again as other URNs don't exist
			false,
			[]urns.URN{"telegram:100001?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30002"},
			models.NilChannelID,
			[]assets.GroupUUID{"dcc16d85-8274-4d19-a3c2-152d4ee99380"},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:100002"), urns.URN("telegram:100001")},
			prevContact(), // same again as other URNs don't exist
			false,
			[]urns.URN{"telegram:100001?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30002"},
			models.NilChannelID,
			[]assets.GroupUUID{"dcc16d85-8274-4d19-a3c2-152d4ee99380"},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:200001"), urns.URN("telegram:100001")},
			prevContact(), // same again as other URNs are orphaned
			false,
			[]urns.URN{"telegram:100001?channel=74729f45-7f29-4868-9dc4-90e491e3c7d8&id=30002"},
			models.NilChannelID,
			[]assets.GroupUUID{"dcc16d85-8274-4d19-a3c2-152d4ee99380"},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:100003"), urns.URN("telegram:100004")}, // 2 new URNs
			newContact(),
			true,
			[]urns.URN{"telegram:100003?id=30003", "telegram:100004?id=30004"},
			models.NilChannelID,
			[]assets.GroupUUID{},
		},
		{
			testdb.Org1.ID,
			[]urns.URN{urns.URN("telegram:100005"), urns.URN("telegram:200002")}, // 1 new, 1 orphaned
			newContact(),
			true,
			[]urns.URN{"telegram:100005?id=30005", "telegram:200002?id=30001"},
			models.NilChannelID,
			[]assets.GroupUUID{},
		},
	}

	for i, tc := range tcs {
		contact, flowContact, created, err := models.GetOrCreateContact(ctx, rt.DB, oa, testdb.Admin.ID, tc.URNs, tc.ChannelID)
		assert.NoError(t, err, "%d: error creating contact", i)

		assert.Equal(t, tc.ContactID, contact.ID(), "%d: contact id mismatch", i)
		assert.Equal(t, tc.ContactURNs, flowContact.URNs().RawURNs(), "%d: URNs mismatch", i)
		assert.Equal(t, tc.Created, created, "%d: created flag mismatch", i)

		groupUUIDs := make([]assets.GroupUUID, len(flowContact.Groups().All()))
		for i, g := range flowContact.Groups().All() {
			groupUUIDs[i] = g.UUID()
		}

		assert.Equal(t, tc.GroupsUUIDs, groupUUIDs, "%d: groups mismatch", i)
	}
}

func TestGetOrCreateContactRace(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	mdb := testsuite.NewMockDB(rt.DB, func(funcName string, call int) error {
		// Make beginning a transaction take a while to create race condition. All threads will fetch
		// URN owners and decide nobody owns the URN, so all threads will try to create a new contact.
		if funcName == "BeginTxx" {
			time.Sleep(100 * time.Millisecond)
		}
		return nil
	})

	var contacts [2]*models.Contact
	var errs [2]error

	test.RunConcurrently(2, func(i int) {
		contacts[i], _, _, errs[i] = models.GetOrCreateContact(ctx, mdb, oa, testdb.Admin.ID, []urns.URN{urns.URN("telegram:100007")}, models.NilChannelID)
	})

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, contacts[0].ID(), contacts[1].ID())
}

func TestGetOrCreateContactIDsFromURNs(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	// add an orphaned URN
	testdb.InsertContactURN(rt, testdb.Org1, nil, urns.URN("telegram:200001"), 100, nil)

	ann, _, _ := testdb.Ann.Load(rt, oa)

	tcs := []struct {
		orgID   models.OrgID
		urns    []urns.URN
		fetched map[urns.URN]*models.Contact
		created []urns.URN
	}{
		{
			orgID: testdb.Org1.ID,
			urns:  []urns.URN{testdb.Ann.URN},
			fetched: map[urns.URN]*models.Contact{
				testdb.Ann.URN: ann,
			},
			created: []urns.URN{},
		},
		{
			orgID: testdb.Org1.ID,
			urns:  []urns.URN{urns.URN(testdb.Ann.URN.String() + "?foo=bar")},
			fetched: map[urns.URN]*models.Contact{
				urns.URN(testdb.Ann.URN.String() + "?foo=bar"): ann,
			},
			created: []urns.URN{},
		},
		{
			orgID: testdb.Org1.ID,
			urns:  []urns.URN{testdb.Ann.URN, urns.URN("telegram:100001")},
			fetched: map[urns.URN]*models.Contact{
				testdb.Ann.URN: ann,
			},
			created: []urns.URN{"telegram:100001"},
		},
		{
			orgID:   testdb.Org1.ID,
			urns:    []urns.URN{urns.URN("telegram:200001")},
			fetched: map[urns.URN]*models.Contact{},
			created: []urns.URN{"telegram:200001"}, // new contact assigned orphaned URN
		},
	}

	for i, tc := range tcs {
		fetched, created, err := models.GetOrCreateContactsFromURNs(ctx, rt.DB, oa, testdb.Admin.ID, tc.urns)
		assert.NoError(t, err, "%d: error getting contact ids", i)
		assert.Equal(t, tc.fetched, fetched, "%d: fetched contacts mismatch", i)
		assert.Equal(t, tc.created, slices.AppendSeq([]urns.URN{}, maps.Keys(created)), "%d: created contacts mismatch", i)
	}
}

func TestGetOrCreateContactsFromURNsRace(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	mdb := testsuite.NewMockDB(rt.DB, func(funcName string, call int) error {
		// Make beginning a transaction take a while to create race condition. All threads will fetch
		// URN owners and decide nobody owns the URN, so all threads will try to create a new contact.
		if funcName == "BeginTxx" {
			time.Sleep(100 * time.Millisecond)
		}
		return nil
	})

	var contacts [2]*models.Contact
	var errs [2]error

	test.RunConcurrently(2, func(i int) {
		var created map[urns.URN]*models.Contact
		_, created, errs[i] = models.GetOrCreateContactsFromURNs(ctx, mdb, oa, testdb.Admin.ID, []urns.URN{urns.URN("telegram:100007")})
		contacts[i] = created[urns.URN("telegram:100007")]
	})

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, contacts[0], contacts[1])
}

func TestGetContactIDsFromReferences(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	ids, err := models.GetContactIDsFromReferences(ctx, rt.DB, testdb.Org1.ID, []*flows.ContactReference{
		flows.NewContactReference(testdb.Ann.UUID, "Ann"),
		flows.NewContactReference(testdb.Bob.UUID, "Bob"),
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, ids)
}

func TestUpdateContactLastSeenAndModifiedOn(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	t0 := time.Now()

	err = models.UpdateContactModifiedOn(ctx, rt.DB, []models.ContactID{testdb.Ann.ID})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE modified_on > $1 AND last_seen_on IS NULL`, t0).Returns(1)

	t1 := time.Now().Truncate(time.Millisecond)
	time.Sleep(time.Millisecond * 5)

	err = models.UpdateContactLastSeenOn(ctx, rt.DB, testdb.Ann.ID, t1)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE modified_on > $1 AND last_seen_on = $1`, t1).Returns(1)

	ann, err := models.LoadContact(ctx, rt.DB, oa, testdb.Ann.ID)
	require.NoError(t, err)
	assert.NotNil(t, ann.LastSeenOn())
	assert.True(t, t1.Equal(*ann.LastSeenOn()))
	assert.True(t, ann.ModifiedOn().After(t1))

	t2 := time.Now().Truncate(time.Millisecond)
	time.Sleep(time.Millisecond * 5)

	// can update directly from the contact object
	err = ann.UpdateLastSeenOn(ctx, rt.DB, t2)
	require.NoError(t, err)
	assert.True(t, t2.Equal(*ann.LastSeenOn()))

	// and that also updates the database
	ann, err = models.LoadContact(ctx, rt.DB, oa, testdb.Ann.ID)
	require.NoError(t, err)
	assert.True(t, t2.Equal(*ann.LastSeenOn()))
	assert.True(t, ann.ModifiedOn().After(t2))
}

func TestUpdateContactStatus(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	err := models.UpdateContactStatus(ctx, rt.DB, []*models.ContactStatusChange{})
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'B'`, testdb.Ann.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`, testdb.Ann.ID).Returns(0)

	changes := make([]*models.ContactStatusChange, 0, 1)
	changes = append(changes, &models.ContactStatusChange{testdb.Ann.ID, flows.ContactStatusBlocked})

	err = models.UpdateContactStatus(ctx, rt.DB, changes)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'B'`, testdb.Ann.ID).Returns(1)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`, testdb.Ann.ID).Returns(0)

	changes = make([]*models.ContactStatusChange, 0, 1)
	changes = append(changes, &models.ContactStatusChange{testdb.Ann.ID, flows.ContactStatusStopped})

	err = models.UpdateContactStatus(ctx, rt.DB, changes)
	assert.NoError(t, err)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'B'`, testdb.Ann.ID).Returns(0)
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contact WHERE id = $1 AND status = 'S'`, testdb.Ann.ID).Returns(1)

}

func TestUpdateContactURNs(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	testdb.InsertContactGroup(rt, testdb.Org1, "e3374234-8131-4f65-9c51-ce84fd7f3bb5", "No URN", `urn = ""`)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	assert.NoError(t, err)

	numInitialURNs := 0
	rt.DB.Get(&numInitialURNs, `SELECT count(*) FROM contacts_contacturn`)

	assertContactURNs := func(contactID models.ContactID, expected []string) {
		var actual []string
		err = rt.DB.Select(&actual, `SELECT identity FROM contacts_contacturn WHERE contact_id = $1 ORDER BY priority DESC`, contactID)
		require.NoError(t, err)
		assert.Equal(t, expected, actual, "URNs mismatch for contact %d", contactID)
	}
	assertModifiedOnUpdated := func(contactID models.ContactID, greaterThan time.Time) {
		var modifiedOn time.Time
		err = rt.DB.Get(&modifiedOn, `SELECT modified_on FROM contacts_contact WHERE id = $1`, contactID)
		require.NoError(t, err)
		assert.Greater(t, modifiedOn, greaterThan, "URNs mismatch for contact %d", contactID)
	}
	assertGroups := func(contactID models.ContactID, expected []string) {
		var actual []string
		err = rt.DB.Select(&actual, `SELECT g.name FROM contacts_contactgroup_contacts gc INNER JOIN contacts_contactgroup g ON g.id = gc.contactgroup_id WHERE gc.contact_id = $1`, contactID)
		require.NoError(t, err)
		assert.ElementsMatch(t, expected, actual)
	}

	assertContactURNs(testdb.Ann.ID, []string{"tel:+16055741111"})
	assertContactURNs(testdb.Bob.ID, []string{"tel:+16055742222"})
	assertContactURNs(testdb.George.ID, []string{"tel:+16055743333"})

	annURN := urns.URN(fmt.Sprintf("tel:+16055741111?id=%d", testdb.Ann.URNID))
	bobURN := urns.URN(fmt.Sprintf("tel:+16055742222?id=%d", testdb.Bob.URNID))

	// give Ann a new higher priority URN
	_, err = models.UpdateContactURNs(ctx, rt.DB, oa, []*models.ContactURNsChanged{{testdb.Ann.ID, testdb.Org1.ID, []urns.URN{"tel:+16055700001", annURN}, nil}})
	assert.NoError(t, err)

	assertContactURNs(testdb.Ann.ID, []string{"tel:+16055700001", "tel:+16055741111"})

	// give Bob a new lower priority URN
	_, err = models.UpdateContactURNs(ctx, rt.DB, oa, []*models.ContactURNsChanged{{testdb.Bob.ID, testdb.Org1.ID, []urns.URN{bobURN, "tel:+16055700002"}, nil}})
	assert.NoError(t, err)

	assertContactURNs(testdb.Bob.ID, []string{"tel:+16055742222", "tel:+16055700002"})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE contact_id IS NULL`).Returns(0) // shouldn't be any orphan URNs
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn`).Returns(numInitialURNs + 2)         // but 2 new URNs

	// remove a URN from Ann
	_, err = models.UpdateContactURNs(ctx, rt.DB, oa, []*models.ContactURNsChanged{{testdb.Ann.ID, testdb.Org1.ID, []urns.URN{"tel:+16055700001"}, nil}})
	assert.NoError(t, err)

	assertContactURNs(testdb.Ann.ID, []string{"tel:+16055700001"})
	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn WHERE contact_id IS NULL`).Returns(1) // now orphaned

	t1 := time.Now()

	// steal a URN from Bob and give to Alexandria
	affected, err := models.UpdateContactURNs(ctx, rt.DB, oa, []*models.ContactURNsChanged{
		{testdb.Ann.ID, testdb.Org1.ID, []urns.URN{"tel:+16055700001", "tel:+16055700002"}, nil},
		{testdb.Alexandra.ID, testdb.Org1.ID, []urns.URN{"tel:+16055742222"}, nil},
	})
	assert.NoError(t, err)
	assert.Len(t, affected, 1)
	assert.Equal(t, testdb.Bob.ID, affected[0].ID())

	assertContactURNs(testdb.Ann.ID, []string{"tel:+16055700001", "tel:+16055700002"})
	assertContactURNs(testdb.Alexandra.ID, []string{"tel:+16055742222"})
	assertContactURNs(testdb.Bob.ID, []string(nil))
	assertModifiedOnUpdated(testdb.Bob.ID, t1)
	assertGroups(testdb.Bob.ID, []string{"\\Active", "No URN"})

	// steal the URN back from Alexandria whilst simulataneously adding new URN to Ann and not-changing anything for George
	affected, err = models.UpdateContactURNs(ctx, rt.DB, oa, []*models.ContactURNsChanged{
		{testdb.Bob.ID, testdb.Org1.ID, []urns.URN{"tel:+16055742222", "tel:+16055700002"}, nil},
		{testdb.Ann.ID, testdb.Org1.ID, []urns.URN{"tel:+16055700001", "tel:+16055700003"}, nil},
		{testdb.George.ID, testdb.Org1.ID, []urns.URN{"tel:+16055743333"}, nil},
	})
	assert.NoError(t, err)
	assert.Len(t, affected, 1)
	assert.Equal(t, testdb.Alexandra.ID, affected[0].ID())

	assertContactURNs(testdb.Ann.ID, []string{"tel:+16055700001", "tel:+16055700003"})
	assertContactURNs(testdb.Bob.ID, []string{"tel:+16055742222", "tel:+16055700002"})
	assertContactURNs(testdb.George.ID, []string{"tel:+16055743333"})

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM contacts_contacturn`).Returns(numInitialURNs + 3)
}

func TestLoadContactURNs(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(rt)
	_, _, annURNs := testdb.Ann.Load(rt, oa)
	_, _, bobURNs := testdb.Bob.Load(rt, oa)

	urns, err := models.LoadContactURNs(ctx, rt.DB, []models.URNID{annURNs[0].ID, bobURNs[0].ID})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []*models.ContactURN{annURNs[0], bobURNs[0]}, urns)
}
