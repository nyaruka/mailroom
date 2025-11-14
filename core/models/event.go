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
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
)

const (
	// If event .Data exceeds this number of bytes we compress it - aim is to get as many events written for 1 WCU (1KB)
	eventDataGZThreshold = 900

	eternity time.Duration = -1

	eventTagDeletion = "del"
)

var eventPersistence = map[string]time.Duration{
	events.TypeAirtimeTransferred:     eternity,
	events.TypeCallCreated:            eternity,
	events.TypeCallMissed:             eternity,
	events.TypeCallReceived:           eternity,
	events.TypeChatStarted:            eternity,
	events.TypeContactFieldChanged:    time.Hour * 24 * 365, // 1 year
	events.TypeContactGroupsChanged:   time.Hour * 24 * 365, // 1 year
	events.TypeContactLanguageChanged: time.Hour * 24 * 365, // 1 year
	events.TypeContactNameChanged:     time.Hour * 24 * 365, // 1 year
	events.TypeContactStatusChanged:   eternity,
	events.TypeContactURNsChanged:     time.Hour * 24 * 365, // 1 year
	events.TypeIVRCreated:             eternity,
	events.TypeMsgCreated:             eternity,
	events.TypeMsgDeleted:             time.Hour * 24, // 1 day
	events.TypeMsgReceived:            eternity,
	events.TypeOptInRequested:         eternity,
	events.TypeOptInStarted:           eternity,
	events.TypeOptInStopped:           eternity,
	events.TypeRunEnded:               eternity,
	events.TypeRunStarted:             eternity,
	events.TypeTicketAssigneeChanged:  eternity,
	events.TypeTicketClosed:           eternity,
	events.TypeTicketNoteAdded:        eternity,
	events.TypeTicketOpened:           eternity,
	events.TypeTicketReopened:         eternity,
	events.TypeTicketTopicChanged:     eternity,
}

// Event wraps an engine event for persistence in the history table
type Event struct {
	flows.Event

	OrgID       OrgID
	ContactUUID flows.ContactUUID
	User        *assets.UserReference
}

// DynamoKey returns the PK+SK combo used for persistence
func (e *Event) DynamoKey() DynamoKey {
	return DynamoKey{
		PK: fmt.Sprintf("con#%s", e.ContactUUID),
		SK: fmt.Sprintf("evt#%s", e.UUID()),
	}
}

// DynamoTTL returns the TTL for this event or nil if it should never expire
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

	if e.User != nil {
		data["_user"] = map[string]any{"uuid": e.User.UUID, "name": e.User.Name}
	}

	return attributevalue.MarshalMap(&DynamoItem{
		DynamoKey: e.DynamoKey(),
		OrgID:     e.OrgID,
		TTL:       e.DynamoTTL(),
		Data:      data,
		DataGZ:    dataGz,
	})
}

// PersistEvent returns whether an event should be persisted
func PersistEvent(e flows.Event) bool {
	_, ok := eventPersistence[e.Type()]
	return ok
}

// EventTag is a record of additional information associated with an existing event
type EventTag struct {
	OrgID       OrgID
	ContactUUID flows.ContactUUID
	EventUUID   flows.EventUUID
	Tag         string
	Data        map[string]any
}

// DynamoKey returns the PK+SK combo used for persistence
func (t *EventTag) DynamoKey() DynamoKey {
	return DynamoKey{PK: fmt.Sprintf("con#%s", t.ContactUUID), SK: fmt.Sprintf("evt#%s#%s", t.EventUUID, t.Tag)}
}

// DynamoTTL returns the TTL for this tag or nil if it should never expire
func (t *EventTag) DynamoTTL() *time.Time {
	return nil
}

func (t *EventTag) MarshalDynamo() (map[string]types.AttributeValue, error) {
	return attributevalue.MarshalMap(&DynamoItem{
		DynamoKey: t.DynamoKey(),
		OrgID:     t.OrgID,
		TTL:       t.DynamoTTL(),
		Data:      t.Data,
	})
}

func NewMsgDeletionTag(orgID OrgID, contactUUID flows.ContactUUID, msgUUID flows.EventUUID, byContact bool, u *User) *EventTag {
	data := map[string]any{"created_on": dates.Now()}

	if byContact {
		data["by_contact"] = true
	} else if u != nil {
		data["user"] = map[string]any{"uuid": u.UUID(), "name": u.Name()}
	}

	return &EventTag{
		OrgID:       orgID,
		ContactUUID: contactUUID,
		EventUUID:   msgUUID,
		Tag:         eventTagDeletion,
		Data:        data,
	}
}
