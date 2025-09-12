package models

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
)

// Describes the common format for all items stored in DynamoDB.

// DynamoKey is the key type for all items, consisting of a partition key (PK) and a sort key (SK).
type DynamoKey struct {
	PK string `dynamodbav:"PK"`
	SK string `dynamodbav:"SK"`
}

func (k DynamoKey) String() string {
	return fmt.Sprintf("%s[%s]", k.PK, k.SK)
}

// DynamoItem is the common structure for items stored in DynamoDB.
type DynamoItem struct {
	DynamoKey

	OrgID  OrgID          `dynamodbav:"OrgID"`
	TTL    *time.Time     `dynamodbav:"TTL,unixtime,omitempty"`
	Data   map[string]any `dynamodbav:"Data,omitempty"`
	DataGZ []byte         `dynamodbav:"DataGZ,omitempty"`
}

func (i *DynamoItem) GetData() (map[string]any, error) {
	data := map[string]any{}

	if len(i.DataGZ) > 0 {
		r, err := gzip.NewReader(bytes.NewReader(i.DataGZ))
		if err != nil {
			return nil, fmt.Errorf("error creating gzip reader: %w", err)
		}
		defer r.Close()

		if err := json.NewDecoder(r).Decode(&i.Data); err != nil {
			return nil, fmt.Errorf("error decoding gzip data: %w", err)
		}
	}
	if len(i.Data) > 0 {
		maps.Copy(data, i.Data)
	}

	return data, nil
}

// MarshalJSON is only used for testing
func (i *DynamoItem) MarshalJSON() ([]byte, error) {
	var ttl *time.Time
	if i.TTL != nil {
		t := i.TTL.In(time.UTC)
		ttl = &t
	}

	return json.Marshal(struct {
		PK     string         `json:"PK"`
		SK     string         `json:"SK"`
		OrgID  OrgID          `json:"OrgID"`
		TTL    *time.Time     `json:"TTL,omitempty"`
		Data   map[string]any `json:"Data"`
		DataGZ string         `json:"DataGZ,omitempty"`
	}{
		PK:     i.PK,
		SK:     i.SK,
		OrgID:  i.OrgID,
		TTL:    ttl,
		Data:   i.Data,
		DataGZ: base64.StdEncoding.EncodeToString(i.DataGZ),
	})
}

// BulkWriterQueue queues multiple items to a DynamoDB writer.
func BulkWriterQueue[T any](ctx context.Context, w *dynamo.Writer, items []T) error {
	for _, item := range items {
		if _, err := w.Queue(item); err != nil {
			return fmt.Errorf("error queuing item to DynamoDB writer %s: %w", w.Table(), err)
		}
	}
	return nil
}
