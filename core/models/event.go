package models

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/runtime"
)

const (
	eventDynamoTTL       = 14 * 24 * time.Hour // 2 weeks
	eventDataGZThreshold = 1024                // 1KB, if event data exceeds this, we should compress it
)

type Event struct {
	flows.Event

	OrgID       OrgID
	ContactUUID flows.ContactUUID
	UserID      UserID
}

func (r *Event) DynamoKey() DynamoKey {
	return DynamoKey{
		PK: fmt.Sprintf("con#%s", r.ContactUUID),
		SK: fmt.Sprintf("evt#%s", r.UUID()),
	}
}

func (r *Event) MarshalDynamo() (map[string]types.AttributeValue, error) {
	eJSON, err := json.Marshal(r.Event)
	if err != nil {
		return nil, fmt.Errorf("error marshaling event: %w", err)
	}

	var data map[string]any
	var dataGz []byte

	if len(eJSON) < eventDataGZThreshold {
		if err := json.Unmarshal(eJSON, &data); err != nil {
			return nil, fmt.Errorf("error unmarshaling event json: %w", err)
		}

		delete(data, "uuid")      // remove UUID as it's already in the key
		delete(data, "step_uuid") // not needed
	} else {
		buf := &bytes.Buffer{}
		w := gzip.NewWriter(buf)

		if _, err := io.Copy(w, bytes.NewReader(eJSON)); err != nil {
			return nil, fmt.Errorf("error compressing event: %w", err)
		}

		w.Close()
		dataGz = buf.Bytes()
		data = make(map[string]any)
	}

	if r.UserID != NilUserID {
		data["_user_id"] = r.UserID
	}
	if len(data) == 0 {
		data = nil
	}

	return attributevalue.MarshalMap(&DynamoItem{
		DynamoKey: r.DynamoKey(),
		OrgID:     r.OrgID,
		TTL:       r.Event.CreatedOn().Add(eventDynamoTTL),
		Data:      data,
		DataGZ:    dataGz,
	})
}

func WriteEvents(ctx context.Context, rt *runtime.Runtime, events []*Event) error {
	return nil // TODO
}
