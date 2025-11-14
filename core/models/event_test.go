package models_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventToDynamo(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	reset := test.MockUniverse()
	defer reset()

	oa := testdb.Org1.Load(t, rt)

	tcs := []struct {
		Event  json.RawMessage `json:"event"`
		UserID models.UserID   `json:"user_id,omitempty"`
		Dynamo json.RawMessage `json:"dynamo"`
	}{}

	testJSON := testsuite.ReadFile(t, "testdata/event_to_dynamo.json")
	jsonx.MustUnmarshal(testJSON, &tcs)

	for i, tc := range tcs {
		evt, err := events.Read(tc.Event)
		require.NoError(t, err, "%d: error reading event in test", i)

		var user *models.User
		if tc.UserID != models.NilUserID {
			user = oa.UserByID(tc.UserID)
		}

		me := &models.Event{
			Event:       evt,
			OrgID:       testdb.Org1.ID,
			ContactUUID: testdb.Ann.UUID,
			User:        user.Reference(),
		}

		actual := tc
		actual.Event = jsonx.MustMarshal(evt)
		actualItem, err := me.MarshalDynamo()
		assert.NoError(t, err, "%d: error marshaling event to dynamo", i)

		actual.Dynamo, err = attributevalue.MarshalMapJSON(actualItem)
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
		EventUUID flows.EventUUID `json:"event_uuid"`
		Tag       string          `json:"tag"`
		Data      map[string]any  `json:"data"`
		Dynamo    json.RawMessage `json:"dynamo"`
	}{}

	testJSON := testsuite.ReadFile(t, "testdata/eventtag_to_dynamo.json")
	jsonx.MustUnmarshal(testJSON, &tcs)

	for i, tc := range tcs {
		me := &models.EventTag{
			OrgID:       testdb.Org1.ID,
			ContactUUID: testdb.Ann.UUID,
			EventUUID:   tc.EventUUID,
			Tag:         tc.Tag,
			Data:        tc.Data,
		}

		actual := tc
		actualItem, err := me.MarshalDynamo()
		assert.NoError(t, err, "%d: error marshaling event to dynamo", i)

		actual.Dynamo, err = attributevalue.MarshalMapJSON(actualItem)
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

		err = os.WriteFile("testdata/eventtag_to_dynamo.json", testJSON, 0600)
		require.NoError(t, err)
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
		"user":       map[string]any{"name": "Andy Admin", "uuid": assets.UserUUID("e29fdf9f-56ab-422a-b77d-e3ec26091a25")},
	}, tag.Data)

	tag = models.NewMsgDeletionTag(testdb.Org1.ID, testdb.Ann.UUID, "0197b335-6ded-79a4-95a6-3af85b57f108", true, nil)
	assert.Equal(t, "del", tag.Tag)
	assert.Equal(t, map[string]any{
		"created_on": time.Date(2025, time.May, 4, 12, 30, 46, 123456789, time.UTC),
		"by_contact": true,
	}, tag.Data)
}
