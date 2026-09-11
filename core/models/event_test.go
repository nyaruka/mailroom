package models_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistEvent(t *testing.T) {
	assert.True(t, models.PersistEvent(events.NewContactNameChanged("Bobby")))
	assert.True(t, models.PersistEvent(events.NewContactStatusChanged(core.ContactStatusBlocked)))
	assert.True(t, models.PersistEvent(events.NewError("URN taken by another contact", events.ErrorCodeURNTaken)))
	assert.False(t, models.PersistEvent(events.NewError("Bang", "bang")))
	assert.False(t, models.PersistEvent(events.NewWarning("Don't do that", "")))

	e := events.NewError("URN taken by another contact", events.ErrorCodeURNTaken)
	e.SetUser(nil, string(models.ViaImport))
	assert.False(t, models.PersistEvent(e))
}

func TestPublishEvent(t *testing.T) {
	// persisted events are also published
	assert.True(t, models.PublishEvent(events.NewContactNameChanged("Bobby")))
	assert.True(t, models.PublishEvent(events.NewContactStatusChanged(core.ContactStatusBlocked)))

	// as are ephemeral events that update UI state
	assert.True(t, models.PublishEvent(events.NewContactLastSeenChanged(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC))))
	assert.True(t, models.PublishEvent(events.NewContactFlowChanged(assets.NewFlowReference("50c3706e-fedb-42c0-8eab-dda3335714b7", "Registration"))))
	assert.True(t, models.PublishEvent(events.NewContactFlowChanged(nil)))

	// but not everything else
	assert.False(t, models.PublishEvent(events.NewWarning("Don't do that", "")))
}

func TestEventToDynamo(t *testing.T) {
	reset := test.MockUniverse()
	defer reset()

	tcs := []struct {
		Event  json.RawMessage `json:"event"`
		Dynamo json.RawMessage `json:"dynamo"`
	}{}

	testJSON := testsuite.ReadFile(t, "testdata/event_to_dynamo.json")
	jsonx.MustUnmarshal(testJSON, &tcs)

	for i, tc := range tcs {
		evt, err := events.Read(tc.Event)
		require.NoError(t, err, "%d: error reading event in test", i)

		me := &models.Event{
			Event:       evt,
			OrgID:       testdb.Org1.ID,
			ContactUUID: testdb.Ann.UUID,
		}

		actual := tc
		actual.Event = jsonx.MustMarshal(evt)

		actualItem, err := me.MarshalDynamo()
		assert.NoError(t, err, "%d: error marshaling event to dynamo", i)

		actualMap, err := attributevalue.MarshalMap(actualItem)
		require.NoError(t, err, "%d: error marshaling event to map", i)

		actual.Dynamo, err = attributevalue.MarshalMapJSON(actualMap)
		assert.NoError(t, err, "%d: error marshaling event to JSON", i)

		if !test.UpdateSnapshots {
			test.AssertEqualJSON(t, tc.Dynamo, actual.Dynamo, "%d: dynamo mismatch", i)
		} else {
			tcs[i] = actual
		}
	}

	if test.UpdateSnapshots {
		testJSON, err := jsonx.MarshalPretty(tcs)
		require.NoError(t, err)

		err = os.WriteFile("testdata/event_to_dynamo.json", testJSON, 0600)
		require.NoError(t, err)
	}
}

func TestEventTagToDynamo(t *testing.T) {
	tcs := []struct {
		EventUUID events.EventUUID `json:"event_uuid"`
		Tag       string           `json:"tag"`
		Qualifier string           `json:"qualifier,omitempty"`
		Data      map[string]any   `json:"data"`
		TTL       *time.Time       `json:"ttl,omitempty"`
		Dynamo    json.RawMessage  `json:"dynamo"`
	}{}

	testJSON := testsuite.ReadFile(t, "testdata/eventtag_to_dynamo.json")
	jsonx.MustUnmarshal(testJSON, &tcs)

	for i, tc := range tcs {
		me := &models.EventTag{
			OrgID:       testdb.Org1.ID,
			ContactUUID: testdb.Ann.UUID,
			EventUUID:   tc.EventUUID,
			Tag:         tc.Tag,
			Qualifier:   tc.Qualifier,
			Data:        tc.Data,
			TTL:         tc.TTL,
		}

		actual := tc
		actualItem, err := me.MarshalDynamo()
		assert.NoError(t, err, "%d: error marshaling tag to dynamo", i)

		actualMap, err := attributevalue.MarshalMap(actualItem)
		require.NoError(t, err, "%d: error marshaling tag to map", i)

		actual.Dynamo, err = attributevalue.MarshalMapJSON(actualMap)
		assert.NoError(t, err, "%d: error marshaling tag to JSON", i)

		if !test.UpdateSnapshots {
			test.AssertEqualJSON(t, tc.Dynamo, actual.Dynamo, "%d: dynamo mismatch", i)
		} else {
			tcs[i] = actual
		}
	}

	if test.UpdateSnapshots {
		testJSON, err := jsonx.MarshalPretty(tcs)
		require.NoError(t, err)

		err = os.WriteFile("testdata/eventtag_to_dynamo.json", testJSON, 0600)
		require.NoError(t, err)
	}
}

func TestNewMsgStatusTag(t *testing.T) {
	reset := test.MockUniverse()
	defer reset()

	// each status is its own item, keyed by the status code, so that writes for the same message from different
	// instances can't overwrite each other. Non-terminal statuses expire after 90 days, read after a year and
	// failed never - a message with no status items is rendered as sent, so only failed would be misrepresented.
	tag := models.NewMsgStatusTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", models.MsgStatusSent, models.NilMsgFailedReason)
	assert.Equal(t, testdb.Org1.ID, tag.OrgID)
	assert.Equal(t, testdb.Ann.UUID, tag.ContactUUID)
	assert.Equal(t, events.EventUUID("0197b335-6ded-79a4-95a6-3af85b57f108"), tag.EventUUID)
	assert.Equal(t, "sts", tag.Tag)
	assert.Equal(t, "S", tag.Qualifier)
	assert.Equal(t, "evt#0197b335-6ded-79a4-95a6-3af85b57f108#sts#S", tag.DynamoKey().SK)
	assert.Equal(t, map[string]any{
		"created_on": time.Date(2025, time.May, 4, 12, 30, 45, 123456789, time.UTC),
		"status":     "sent",
	}, tag.Data)
	if assert.NotNil(t, tag.TTL) {
		assert.Equal(t, time.Date(2025, time.August, 2, 12, 30, 45, 123456789, time.UTC), *tag.TTL) // created_on + 90 days
	}

	tag = models.NewMsgStatusTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", models.MsgStatusFailed, models.MsgFailedTooOld)
	assert.Equal(t, "sts", tag.Tag)
	assert.Equal(t, "F", tag.Qualifier)
	assert.Equal(t, "evt#0197b335-6ded-79a4-95a6-3af85b57f108#sts#F", tag.DynamoKey().SK)
	assert.Equal(t, map[string]any{
		"created_on": time.Date(2025, time.May, 4, 12, 30, 46, 123456789, time.UTC),
		"status":     "failed",
		"reason":     "too_old",
	}, tag.Data)
	assert.Nil(t, tag.TTL)

	// every status a message can be in when we tag it must map to a non-empty external name (consumed as the
	// event's _status by clients) - an unmapped one would silently write `"status": ""` and render as an empty
	// badge with no error. These names, and the TTLs, are shared with courier, which writes the same items for the
	// statuses it records, so they can't be changed on one side alone.
	statuses := map[models.MsgStatus]struct {
		name string
		ttl  time.Duration
	}{
		models.MsgStatusWired:     {"wired", 90 * 24 * time.Hour},
		models.MsgStatusSent:      {"sent", 90 * 24 * time.Hour},
		models.MsgStatusDelivered: {"delivered", 90 * 24 * time.Hour},
		models.MsgStatusRead:      {"read", 365 * 24 * time.Hour},
		models.MsgStatusErrored:   {"errored", 90 * 24 * time.Hour},
		models.MsgStatusFailed:    {"failed", 0},
	}

	for status, expected := range statuses {
		tag := models.NewMsgStatusTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", status, models.NilMsgFailedReason)
		assert.Equal(t, "sts", tag.Tag)
		assert.Equal(t, string(status), tag.Qualifier)
		assert.Equal(t, "evt#0197b335-6ded-79a4-95a6-3af85b57f108#sts#"+string(status), tag.DynamoKey().SK)
		assert.Equal(t, expected.name, tag.Data["status"], "unexpected name for status %q", status)
		assert.NotContains(t, tag.Data, "reason")

		if expected.ttl == 0 {
			assert.Nil(t, tag.TTL, "unexpected TTL for status %q", status)
		} else if assert.NotNil(t, tag.TTL, "expected TTL for status %q", status) {
			assert.Equal(t, tag.Data["created_on"].(time.Time).Add(expected.ttl), *tag.TTL, "unexpected TTL for status %q", status)
		}
	}

	// only the failure reasons which can be set after the message is created get a reason
	reasons := map[models.MsgFailedReason]string{
		models.MsgFailedErrorLimit:     "error_limit",
		models.MsgFailedTooOld:         "too_old",
		models.MsgFailedChannelRemoved: "channel_removed",
		models.MsgFailedNoDestination:  "no_destination",
		models.MsgFailedContact:        "",
		models.MsgFailedSuspended:      "",
		models.MsgFailedLooping:        "",
	}

	for failedReason, expected := range reasons {
		tag := models.NewMsgStatusTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", models.MsgStatusFailed, failedReason)
		assert.Equal(t, "failed", tag.Data["status"])

		if expected == "" {
			assert.NotContains(t, tag.Data, "reason", "unexpected reason for failed reason %q", failedReason)
		} else {
			assert.Equal(t, expected, tag.Data["reason"], "unexpected reason for failed reason %q", failedReason)
		}
	}
}

func TestEventTags(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	reset := test.MockUniverse()
	defer reset()

	oa := testdb.Org1.Load(t, rt)
	admin := oa.UserByID(testdb.Admin.ID)

	tag := models.NewMsgDeletionTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", false, admin)
	assert.Equal(t, "del", tag.Tag)
	assert.Equal(t, map[string]any{
		"created_on": time.Date(2025, time.May, 4, 12, 30, 45, 123456789, time.UTC),
		"user":       map[string]any{"name": "Andy Admin", "uuid": assets.UserUUID("ad9fdf9f-56ab-422a-b77d-e3ec26091a25")},
	}, tag.Data)

	tag = models.NewMsgDeletionTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", true, nil)
	assert.Equal(t, "del", tag.Tag)
	assert.Equal(t, map[string]any{
		"created_on": time.Date(2025, time.May, 4, 12, 30, 46, 123456789, time.UTC),
		"by_contact": true,
	}, tag.Data)

	// airtime status tags are unchanged: a single unqualified item per transfer that the latest change overwrites
	tag = models.NewAirtimeStatusTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", models.AirtimeTransferStatusCompleted)
	assert.Equal(t, "sts", tag.Tag)
	assert.Equal(t, "", tag.Qualifier)
	assert.Equal(t, "evt#0197b335-6ded-79a4-95a6-3af85b57f108#sts", tag.DynamoKey().SK)
	assert.Nil(t, tag.TTL)
	assert.Equal(t, map[string]any{
		"created_on": time.Date(2025, time.May, 4, 12, 30, 47, 123456789, time.UTC),
		"status":     "completed",
	}, tag.Data)
}
