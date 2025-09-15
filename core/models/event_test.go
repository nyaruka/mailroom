package models_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvent(t *testing.T) {
	reset := test.MockUniverse()
	defer reset()

	tcs := []struct {
		Event    json.RawMessage `json:"event"`
		UserUUID assets.UserUUID `json:"user_uuid,omitempty"`
		Dynamo   json.RawMessage `json:"dynamo"`
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
			UserUUID:    tc.UserUUID,
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
