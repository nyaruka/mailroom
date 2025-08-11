package models

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
)

var EventPersistence = map[string]time.Duration{
	events.TypeAirtimeTransferred:     -1,                   // forever
	events.TypeContactFieldChanged:    time.Hour * 24 * 365, // 1 year
	events.TypeContactGroupsChanged:   time.Hour * 24 * 365, // 1 year
	events.TypeContactLanguageChanged: time.Hour * 24 * 365, // 1 year
	events.TypeContactNameChanged:     time.Hour * 24 * 365, // 1 year
	events.TypeContactURNsChanged:     time.Hour * 24 * 365, // 1 year
}

const (
	eventDataGZThreshold = 1024 // 1KB, if event data exceeds this, we should compress it
)

type Event struct {
	flows.Event

	OrgID       OrgID
	ContactUUID flows.ContactUUID
	UserID      UserID
}

func (e *Event) DynamoKey() DynamoKey {
	return DynamoKey{
		PK: fmt.Sprintf("con#%s", e.ContactUUID),
		SK: fmt.Sprintf("evt#%s", e.UUID()),
	}
}

func (e *Event) DynamoTTL() *time.Time {
	if persistence := EventPersistence[e.Type()]; persistence > 0 {
		ttl := e.CreatedOn().Add(persistence)
		return &ttl
	}
	return nil
}

func (e *Event) MarshalDynamo() (map[string]types.AttributeValue, error) {
	eJSON, err := json.Marshal(e.Event)
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

	if e.UserID != NilUserID {
		data["_user_id"] = e.UserID
	}
	if len(data) == 0 {
		data = nil
	}

	return attributevalue.MarshalMap(&DynamoItem{
		DynamoKey: e.DynamoKey(),
		OrgID:     e.OrgID,
		TTL:       e.DynamoTTL(),
		Data:      data,
		DataGZ:    dataGz,
	})
}
