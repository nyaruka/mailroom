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
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
)

var eventPersistence = map[string]time.Duration{
	events.TypeAirtimeTransferred:     -1,
	events.TypeCallCreated:            -1,
	events.TypeCallMissed:             -1,
	events.TypeCallReceived:           -1,
	events.TypeChatStarted:            -1,
	events.TypeContactFieldChanged:    time.Hour * 24 * 365, // 1 year
	events.TypeContactGroupsChanged:   time.Hour * 24 * 365, // 1 year
	events.TypeContactLanguageChanged: time.Hour * 24 * 365, // 1 year
	events.TypeContactNameChanged:     time.Hour * 24 * 365, // 1 year
	events.TypeContactStatusChanged:   -1,
	events.TypeContactURNsChanged:     time.Hour * 24 * 365, // 1 year
	events.TypeOptInStarted:           -1,
	events.TypeOptInStopped:           -1,
	events.TypeRunEnded:               -1,
	events.TypeRunStarted:             -1,
}

const (
	// If event .Data exceeds this number of bytes we compress it - aim is to get as many events written for 1 WCU (1KB)
	eventDataGZThreshold = 900
)

type Event struct {
	flows.Event

	OrgID       OrgID
	ContactUUID flows.ContactUUID
	UserUUID    assets.UserUUID
}

func (e *Event) DynamoKey() DynamoKey {
	return DynamoKey{
		PK: fmt.Sprintf("con#%s", e.ContactUUID),
		SK: fmt.Sprintf("evt#%s", e.UUID()),
	}
}

func (e *Event) DynamoTTL() *time.Time {
	if persistence := eventPersistence[e.Type()]; persistence > 0 {
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
		data = make(map[string]any, 2)
		data["type"] = e.Type() // always have type in uncompressed data
	}

	if e.UserUUID != "" {
		data["_user_uuid"] = e.UserUUID
	}

	return attributevalue.MarshalMap(&DynamoItem{
		DynamoKey: e.DynamoKey(),
		OrgID:     e.OrgID,
		TTL:       e.DynamoTTL(),
		Data:      data,
		DataGZ:    dataGz,
	})
}

// UnmarshalDynamo is only used in tests
func (e *Event) UnmarshalDynamo(d map[string]types.AttributeValue) error {
	item := &DynamoItem{}
	if err := attributevalue.UnmarshalMap(d, item); err != nil {
		return fmt.Errorf("error unmarshaling item: %w", err)
	}

	data, err := item.GetData()
	if err != nil {
		return fmt.Errorf("error extracting item data: %w", err)
	}

	e.OrgID = item.OrgID
	e.ContactUUID = flows.ContactUUID(item.PK)[4:] // trim off con#
	userUUID, ok := data["_user_uuid"].(string)
	if ok {
		e.UserUUID = assets.UserUUID(userUUID)
		delete(data, "_user_uuid")
	}

	data["uuid"] = item.SK[4:] // trim off evt# and put event UUID back in

	evt, err := events.Read(jsonx.MustMarshal(data))
	if err != nil {
		return fmt.Errorf("error reading event: %w", err)
	}
	e.Event = evt

	return nil
}

// PersistEvent returns whether an event should be persisted
func PersistEvent(e flows.Event) bool {
	_, ok := eventPersistence[e.Type()]
	return ok
}
