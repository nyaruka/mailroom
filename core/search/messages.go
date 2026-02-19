package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nyaruka/gocommon/elastic"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

const (
	// MessageTextMinLength is the minimum length of message text to be indexed
	MessageTextMinLength = 2
)

// MessageDoc represents a message document in the OpenSearch messages index
type MessageDoc struct {
	Timestamp   time.Time         `json:"@timestamp"`
	OrgID       models.OrgID      `json:"org_id"`
	UUID        flows.EventUUID   `json:"uuid"`
	ContactUUID flows.ContactUUID `json:"contact_uuid"`
	Text        string            `json:"text"`
}

// SearchMessages searches the OpenSearch messages index for messages matching the given text in the given org.
func SearchMessages(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, text string) ([]MessageDoc, int, error) {
	if rt.Search == nil {
		return nil, 0, fmt.Errorf("OpenSearch not configured")
	}

	src := map[string]any{
		"query":            elastic.All(elastic.Term("org_id", orgID), elastic.Match("text", text)),
		"sort":             []any{"_score", elastic.SortBy("@timestamp", false)},
		"size":             50,
		"track_total_hits": true,
	}

	resp, err := rt.Search.Messages.Client().Search(ctx, &opensearchapi.SearchReq{
		Indices: []string{rt.Search.Messages.Index()},
		Body:    bytes.NewReader(jsonx.MustMarshal(src)),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("error searching messages: %w", err)
	}

	docs := make([]MessageDoc, len(resp.Hits.Hits))
	for i, hit := range resp.Hits.Hits {
		if err := json.Unmarshal(hit.Source, &docs[i]); err != nil {
			return nil, 0, fmt.Errorf("error unmarshalling message doc: %w", err)
		}
	}

	return docs, resp.Hits.Total.Value, nil
}
