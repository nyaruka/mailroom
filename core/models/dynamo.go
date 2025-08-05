package models

import (
	"context"
	"fmt"
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
	TTL    time.Time      `dynamodbav:"TTL,unixtime,omitempty"`
	Data   map[string]any `dynamodbav:"Data,omitempty"`
	DataGZ []byte         `dynamodbav:"DataGZ,omitempty"`
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
