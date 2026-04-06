package testsuite

import (
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

func GetHistoryItems(t *testing.T, rt *runtime.Runtime, clear bool, after time.Time) []*dynamo.Item {
	t.Helper()

	rt.Dynamo.History.Flush()

	table := rt.Dynamo.History.Table()

	allItems := dyntest.ScanAll(t, rt.Dynamo.History.Client(), table)

	if after.IsZero() {
		if clear {
			dyntest.Truncate(t, rt.Dynamo.History.Client(), table)
		}
		return allItems
	}

	// filter items by UUID7 time boundary
	afterMs := after.UnixMilli()
	items := make([]*dynamo.Item, 0, len(allItems))

	for _, item := range allItems {
		if skUUID7TimeMs(item.SK) >= afterMs {
			items = append(items, item)
		}
	}

	if clear && len(items) > 0 {
		client := rt.Dynamo.History.Client()
		for _, item := range items {
			_, err := client.DeleteItem(t.Context(), &dynamodb.DeleteItemInput{
				TableName: aws.String(table),
				Key: map[string]dbtypes.AttributeValue{
					"PK": &dbtypes.AttributeValueMemberS{Value: item.PK},
					"SK": &dbtypes.AttributeValueMemberS{Value: item.SK},
				},
			})
			require.NoError(t, err)
		}
	}

	return items
}

// skUUID7TimeMs extracts the millisecond timestamp from a sort key like "evt#<uuid7>" or "evt#<uuid7>#<tag>"
func skUUID7TimeMs(sk string) int64 {
	if len(sk) < 40 { // "evt#" (4) + UUID (36)
		return 0
	}
	// UUID7 first 48 bits = 12 hex chars at positions 0-7 and 9-12 of the UUID
	hex := sk[4:12] + sk[13:17]
	ms, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		return 0
	}
	return ms
}

func GetHistoryEventTypes(t *testing.T, rt *runtime.Runtime, clear bool, after time.Time) map[flows.ContactUUID][]string {
	items := GetHistoryItems(t, rt, clear, after)

	evtTypes := make(map[flows.ContactUUID][]string, len(items))

	for _, item := range items {
		data, err := item.GetData()
		require.NoError(t, err)

		evtType, ok := data["type"]
		if ok {
			contactUUID := flows.ContactUUID(item.PK)[4:] // trim off con#
			evtTypes[contactUUID] = append(evtTypes[contactUUID], evtType.(string))
		}
	}

	return evtTypes
}
