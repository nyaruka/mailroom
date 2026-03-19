package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	// MessageTextMinLength is the minimum length of message text to be indexed
	MessageTextMinLength = 2
)

// MessageDoc represents a message document in the Elasticsearch messages index. UUID is used as the document _id.
type MessageDoc struct {
	CreatedOn   time.Time         `json:"@timestamp"` // also used to determine monthly index
	UUID        flows.EventUUID   `json:"-"`          // used as _id
	OrgID       models.OrgID      `json:"org_id"`
	ContactUUID flows.ContactUUID `json:"contact_uuid"`
	Text        string            `json:"text"`
	InTicket    bool              `json:"in_ticket"`
}

// IndexName returns the monthly index name for this message, e.g. base "messages" with a message
// from January 2026 gives "messages-2026-01".
func (m *MessageDoc) IndexName(base string) string {
	return fmt.Sprintf("%s-%s", base, m.CreatedOn.UTC().Format("2006-01"))
}

// MessageResult is a single result from a message search containing the contact UUID and event data.
type MessageResult struct {
	ContactUUID flows.ContactUUID
	Event       map[string]any
}

// SearchMessages searches the Elasticsearch messages index for messages matching the given text in the given org,
// then fetches the corresponding events from DynamoDB.
func SearchMessages(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, text string, contactUUID flows.ContactUUID, inTicket bool, limit int) ([]MessageResult, error) {
	routing := fmt.Sprintf("%d", orgID)

	// orgwide search looks back 180 days, but if we're filtering by contact we can look back a full year
	since := "now-180d/d"
	if contactUUID != "" {
		since = "now-1y/d"
	}

	filter := []map[string]any{
		{"term": map[string]any{"org_id": orgID}},
		{"range": map[string]any{"@timestamp": map[string]string{"gte": since}}},
	}
	if contactUUID != "" {
		filter = append(filter, map[string]any{"term": map[string]any{"contact_uuid": contactUUID}})
	}
	if inTicket {
		filter = append(filter, map[string]any{"term": map[string]any{"in_ticket": true}})
	}

	src := map[string]any{
		"query": map[string]any{
			"bool": map[string]any{
				"filter": filter,
				"must": []map[string]any{
					{"match": map[string]any{"text": map[string]any{"query": text, "operator": "and", "fuzziness": "AUTO"}}},
				},
			},
		},
		"sort":             []any{"_score", map[string]string{"@timestamp": "desc"}},
		"size":             limit,
		"track_total_hits": false,
	}

	index := rt.Config.ElasticMessagesIndex + "-*"

	results, err := rt.ES.Client.Search().Index(index).Routing(routing).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("error searching messages: %w", err)
	}

	type hitResult struct {
		uuid        flows.EventUUID
		contactUUID flows.ContactUUID
	}

	hits := make([]hitResult, len(results.Hits.Hits))
	for i, hit := range results.Hits.Hits {
		var doc MessageDoc
		if err := json.Unmarshal(hit.Source_, &doc); err != nil {
			return nil, fmt.Errorf("error unmarshalling message doc: %w", err)
		}
		hits[i] = hitResult{uuid: flows.EventUUID(*hit.Id_), contactUUID: doc.ContactUUID}
	}

	// build DynamoDB keys from Elasticsearch results
	keys := make([]dynamo.Key, len(hits))
	for i, hit := range hits {
		keys[i] = dynamo.Key{
			PK: fmt.Sprintf("con#%s", hit.contactUUID),
			SK: fmt.Sprintf("evt#%s", hit.uuid),
		}
	}

	// batch fetch events from DynamoDB
	items, _, err := dynamo.BatchGetItem(ctx, rt.Dynamo.History.Client(), rt.Dynamo.History.Table(), keys)
	if err != nil {
		return nil, fmt.Errorf("error fetching events from DynamoDB: %w", err)
	}

	// index items by SK for ordered lookup
	itemsBySK := make(map[string]*dynamo.Item, len(items))
	for _, item := range items {
		itemsBySK[item.SK] = item
	}

	// build results in Elasticsearch relevance order, skipping any not found in DynamoDB
	msgResults := make([]MessageResult, 0, len(hits))
	for _, hit := range hits {
		item := itemsBySK[fmt.Sprintf("evt#%s", hit.uuid)]
		if item == nil {
			continue
		}

		data, err := item.GetData()
		if err != nil {
			return nil, fmt.Errorf("error getting event data: %w", err)
		}

		data["uuid"] = string(hit.uuid) // re-add uuid (stripped on write)

		msgResults = append(msgResults, MessageResult{ContactUUID: hit.contactUUID, Event: data})
	}

	return msgResults, nil
}

// DeindexMessagesByContact deletes all messages in the Elasticsearch messages index for the given contact UUIDs.
func DeindexMessagesByContact(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactUUIDs []flows.ContactUUID) (int, error) {
	routing := fmt.Sprintf("%d", orgID)
	uuids := make([]string, len(contactUUIDs))
	for i, u := range contactUUIDs {
		uuids[i] = string(u)
	}

	src := map[string]any{
		"query": map[string]any{
			"terms": map[string]any{"contact_uuid": uuids},
		},
	}

	index := rt.Config.ElasticMessagesIndex + "-*"

	resp, err := rt.ES.Client.DeleteByQuery(index).Routing(routing).Raw(bytes.NewReader(jsonx.MustMarshal(src))).Do(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deindexing messages for contacts in org #%d: %w", orgID, err)
	}

	return int(*resp.Deleted), nil
}
