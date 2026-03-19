package testsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	valkey "github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/aws/dynamo/dyntest"
	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/search"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/nyaruka/mailroom/utils/queues"
	"github.com/stretchr/testify/require"
)

func QueueBatchTask(t *testing.T, rt *runtime.Runtime, org *testdb.Org, task tasks.Task) {
	ctx := context.Background()

	err := tasks.Queue(ctx, rt, rt.Queues.Batch, org.ID, task, false)
	require.NoError(t, err)
}

func QueueContactTask(t *testing.T, rt *runtime.Runtime, org *testdb.Org, contact *testdb.Contact, ctask ctasks.Task) {
	ctx := context.Background()

	err := tasks.QueueContact(ctx, rt, org.ID, contact.ID, ctask)
	require.NoError(t, err)
}

func CurrentTasks(t *testing.T, rt *runtime.Runtime, qname string) map[models.OrgID][]*queues.Task {
	vc := rt.VK.Get()
	defer vc.Close()

	queued, err := valkey.Ints(vc.Do("ZRANGE", fmt.Sprintf("{tasks:%s}:queued", qname), 0, -1))
	require.NoError(t, err)

	tasks := make(map[models.OrgID][]*queues.Task)
	for _, orgID := range queued {
		tasks1, err := valkey.Strings(vc.Do("LRANGE", fmt.Sprintf("{tasks:%s}:o:%d/1", qname, orgID), 0, -1))
		require.NoError(t, err)

		tasks0, err := valkey.Strings(vc.Do("LRANGE", fmt.Sprintf("{tasks:%s}:o:%d/0", qname, orgID), 0, -1))
		require.NoError(t, err)

		orgTasks := make([]*queues.Task, len(tasks1)+len(tasks0))

		for i, rawTask := range slices.Concat(tasks1, tasks0) {
			parts := bytes.SplitN([]byte(rawTask), []byte("|"), 2) // split into id and task json

			task := &queues.Task{}
			jsonx.MustUnmarshal(parts[1], task)
			orgTasks[i] = task
		}

		tasks[models.OrgID(orgID)] = orgTasks
	}

	return tasks
}

type TaskInfo struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func GetQueuedTasks(t *testing.T, rt *runtime.Runtime) map[string][]TaskInfo {
	t.Helper()

	actual := make(map[string][]TaskInfo)

	for _, qname := range []string{"realtime", "batch", "throttled"} {
		for orgID, oTasks := range CurrentTasks(t, rt, qname) {
			key := fmt.Sprintf("%s/%d", qname, orgID)
			actual[key] = make([]TaskInfo, len(oTasks))
			for i, task := range oTasks {
				actual[key][i] = TaskInfo{Type: task.Type, Payload: task.Task}
			}
		}
	}

	return actual
}

func GetQueuedTaskTypes(t *testing.T, rt *runtime.Runtime) map[string][]string {
	t.Helper()

	actual := make(map[string][]string)

	for key, tasks := range GetQueuedTasks(t, rt) {
		types := make([]string, len(tasks))
		for i, task := range tasks {
			types[i] = task.Type
		}
		actual[key] = types
	}

	return actual
}

// FlushTasks processes any queued tasks
func FlushTasks(t *testing.T, rt *runtime.Runtime, qnames ...string) map[string]int {
	return drainTasks(t, rt, true, qnames...)
}

// ClearTasks removes any queued tasks without processing them
func ClearTasks(t *testing.T, rt *runtime.Runtime, qnames ...string) map[string]int {
	return drainTasks(t, rt, false, qnames...)
}

func drainTasks(t *testing.T, rt *runtime.Runtime, perform bool, qnames ...string) map[string]int {
	vc := rt.VK.Get()
	defer vc.Close()

	var task *queues.Task
	var err error
	counts := make(map[string]int)

	var qs []queues.Fair
	for _, q := range []queues.Fair{rt.Queues.Realtime, rt.Queues.Batch, rt.Queues.Throttled} {
		if len(qnames) == 0 || slices.Contains(qnames, fmt.Sprint(q)) {
			qs = append(qs, q)
		}
	}

	for {
		// look for a task in the queues
		var q queues.Fair
		for _, q = range qs {
			task, err = q.Pop(t.Context(), vc)
			require.NoError(t, err)

			if task != nil {
				break
			}
		}

		if task == nil { // all done
			break
		}

		counts[task.Type]++

		if perform {
			err = tasks.Perform(t.Context(), rt, task)
			require.NoError(t, err, "unexpected error performing task %s", task.Type)
		}

		err = q.Done(t.Context(), vc, task.OwnerID)
		require.NoError(t, err, "unexpected error marking task %s as done", task.Type)
	}
	return counts
}

// IndexMessages indexes the given messages into Elasticsearch and writes the corresponding events to
// DynamoDB, then refreshes the Elasticsearch index so they're immediately searchable.
func IndexMessages(t *testing.T, rt *runtime.Runtime, msgs []search.MessageDoc) {
	t.Helper()

	for _, msg := range msgs {
		rt.ES.Writer.Queue(&elastic.Document{
			Index:   msg.IndexName(rt.Config.ElasticMessagesIndex),
			ID:      string(msg.UUID),
			Routing: fmt.Sprintf("%d", msg.OrgID),
			Body:    jsonx.MustMarshal(msg),
		})
	}

	rt.ES.Writer.Flush()

	_, err := rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticMessagesIndex + "-*").Do(t.Context())
	require.NoError(t, err)

	for _, msg := range msgs {
		item := &dynamo.Item{
			Key: dynamo.Key{
				PK: fmt.Sprintf("con#%s", msg.ContactUUID),
				SK: fmt.Sprintf("evt#%s", msg.UUID),
			},
			OrgID: int(msg.OrgID),
			Data: map[string]any{
				"type":       "msg_received",
				"text":       msg.Text,
				"created_on": msg.CreatedOn.Format(time.RFC3339),
			},
		}
		err := dynamo.PutItem(t.Context(), rt.Dynamo.History.Client(), rt.Dynamo.History.Table(), item)
		require.NoError(t, err)
	}
}

// IndexedMessage represents an indexed Elasticsearch message for test assertions, including metadata
// fields (_id and _routing) that aren't part of the document body.
type IndexedMessage struct {
	ID          string `json:"_id"`
	Routing     string `json:"_routing"`
	ContactUUID string `json:"contact_uuid"`
	Text        string `json:"text"`
}

func GetIndexedMessages(t *testing.T, rt *runtime.Runtime, clear bool) []IndexedMessage {
	t.Helper()

	rt.ES.Writer.Flush()

	pattern := rt.Config.ElasticMessagesIndex + "-*"

	// check if any message indexes exist
	indexes, err := rt.ES.Client.Cat.Indices().Index(pattern).Do(t.Context())
	require.NoError(t, err)

	if len(indexes) == 0 {
		return nil // no matching indexes yet
	}

	// refresh the indexes to make documents searchable
	_, err = rt.ES.Client.Indices.Refresh().Index(pattern).Do(t.Context())
	require.NoError(t, err)

	// search all documents
	results, err := rt.ES.Client.Search().Index(pattern).Raw(strings.NewReader(`{"query": {"match_all": {}}, "size": 1000}`)).Do(t.Context())
	require.NoError(t, err)

	msgs := make([]IndexedMessage, len(results.Hits.Hits))
	for i, hit := range results.Hits.Hits {
		err := json.Unmarshal(hit.Source_, &msgs[i])
		require.NoError(t, err)
		msgs[i].ID = *hit.Id_
		msgs[i].Routing = *hit.Routing_
	}

	slices.SortFunc(msgs, func(a, b IndexedMessage) int { return strings.Compare(a.ID, b.ID) })

	if clear {
		for _, idx := range indexes {
			if idx.Index != nil {
				_, err := rt.ES.Client.Indices.Delete(*idx.Index).Do(t.Context())
				require.NoError(t, err)
			}
		}
	}

	return msgs
}

// SearchAssertion is a search query and the expected contact IDs that should match.
type SearchAssertion struct {
	Query    string             `json:"query"`
	Contacts []models.ContactID `json:"contacts"`
}

// IndexOrgContacts indexes all active contacts for the given org into the v2 Elastic contacts index
// and refreshes the index so they're immediately searchable.
func IndexOrgContacts(t *testing.T, rt *runtime.Runtime, org *testdb.Org) {
	t.Helper()

	ctx := t.Context()
	oa, err := models.GetOrgAssets(ctx, rt, org.ID)
	require.NoError(t, err)

	contactIDs, err := models.GetContactIDsPage(ctx, rt.DB, org.ID, models.NilContactID, 10_000)
	require.NoError(t, err)

	contacts, err := models.LoadContacts(ctx, rt.DB, oa, contactIDs)
	require.NoError(t, err)

	fcs := make([]*flows.Contact, 0, len(contacts))
	for _, mc := range contacts {
		fc, err := mc.EngineContact(oa)
		require.NoError(t, err)
		fcs = append(fcs, fc)
	}

	err = search.IndexContacts(ctx, rt, oa, fcs, map[models.ContactID]models.FlowID{})
	require.NoError(t, err)

	rt.ES.Writer.Flush()

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndexV2).Do(ctx)
	require.NoError(t, err)
}

// ClearESContactsIndexV2 removes all documents from the v2 Elastic contacts index.
func ClearESContactsIndexV2(t *testing.T, rt *runtime.Runtime) {
	t.Helper()

	_, err := rt.ES.Client.DeleteByQuery(rt.Config.ElasticContactsIndexV2).Raw(strings.NewReader(`{"query": {"match_all": {}}}`)).Do(t.Context())
	require.NoError(t, err)

	_, err = rt.ES.Client.Indices.Refresh().Index(rt.Config.ElasticContactsIndexV2).Do(t.Context())
	require.NoError(t, err)
}

func GetHistoryItems(t *testing.T, rt *runtime.Runtime, clear bool, after time.Time) []*dynamo.Item {
	t.Helper()

	rt.Dynamo.History.Flush()

	allItems := dyntest.ScanAll(t, rt.Dynamo.History.Client(), "TestHistory")

	if after.IsZero() {
		if clear {
			dyntest.Truncate(t, rt.Dynamo.History.Client(), "TestHistory")
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
		table := rt.Dynamo.History.Table()
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
