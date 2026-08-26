package models

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/nyaruka/gocommon/aws/dynamo"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/core/events"
)

type Via string

const (
	ViaUI     Via = "ui"
	ViaAPI    Via = "api"
	ViaImport Via = "import"
)

const (
	// If event .Data exceeds this number of bytes we compress it - aim is to get as many events written for 1 WCU (1KB)
	eventDataGZThreshold = 900

	eternity time.Duration = -1

	eventTagDeletion = "del"
	eventTagStatus   = "sts"
)

var eventPersistence = map[string]time.Duration{
	events.TypeAirtimeCreated:         eternity,
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
	events.TypeError:                  time.Hour * 24 * 365, // 1 year (additional filtering on error code below)
	events.TypeFailure:                eternity,
	events.TypeIVRCreated:             eternity,
	events.TypeMsgCreated:             eternity,
	events.TypeMsgDeleted:             time.Hour * 24, // 1 day
	events.TypeMsgReceived:            eternity,
	events.TypeRunEnded:               eternity,
	events.TypeRunStarted:             eternity,
	events.TypeTicketAssigneeChanged:  eternity,
	events.TypeTicketClosed:           eternity,
	events.TypeTicketNoteAdded:        eternity,
	events.TypeTicketOpened:           eternity,
	events.TypeTicketReopened:         eternity,
	events.TypeTicketTopicChanged:     eternity,
}

// events that are published to history sockets for live subscribers but never persisted to the history table -
// they update UI state (e.g. last seen, current flow) rather than record history
var eventEphemeralPublish = map[string]bool{
	events.TypeContactFlowChanged:     true,
	events.TypeContactLastSeenChanged: true,
}

// PublishEvent returns whether an event should be published to history sockets
func PublishEvent(e events.Event) bool {
	return PersistEvent(e) || eventEphemeralPublish[e.Type()]
}

// PersistEvent returns whether an event should be persisted
func PersistEvent(e events.Event) bool {
	switch typed := e.(type) {
	case *events.Error:
		// Only persist non-import URN taken errors for now - this is to help with flows that still use actions for
		// adding URNs that have no way to route on failure
		return typed.Code == events.ErrorCodeURNTaken && typed.Via_ != string(ViaImport)
	default:
		_, ok := eventPersistence[e.Type()]
		return ok
	}
}

// Event wraps an engine event for persistence in the history table
type Event struct {
	events.Event

	OrgID       OrgID
	ContactUUID core.ContactUUID
}

// DynamoKey returns the PK+SK combo used for persistence
func (e *Event) DynamoKey() dynamo.Key {
	return dynamo.Key{PK: fmt.Sprintf("con#%s", e.ContactUUID), SK: fmt.Sprintf("evt#%s", e.UUID())}
}

// DynamoTTL returns the TTL for this event or nil if it should never expire
func (e *Event) DynamoTTL() *time.Time {
	if persistence := eventPersistence[e.Type()]; persistence > 0 {
		ttl := e.CreatedOn().Add(persistence)
		return &ttl
	}
	return nil
}

func (e *Event) MarshalDynamo() (*dynamo.Item, error) {
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

		delete(data, "uuid") // remove UUID as it's already in the key
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

	return &dynamo.Item{
		Key:    e.DynamoKey(),
		OrgID:  int(e.OrgID),
		TTL:    e.DynamoTTL(),
		Data:   data,
		DataGZ: dataGz,
	}, nil
}

// EventTag is a record of additional information associated with an existing event
type EventTag struct {
	OrgID       OrgID
	ContactUUID core.ContactUUID
	EventUUID   events.EventUUID
	Tag         string
	Data        map[string]any
}

// DynamoKey returns the PK+SK combo used for persistence
func (t *EventTag) DynamoKey() dynamo.Key {
	return dynamo.Key{PK: fmt.Sprintf("con#%s", t.ContactUUID), SK: fmt.Sprintf("evt#%s#%s", t.EventUUID, t.Tag)}
}

func (t *EventTag) MarshalDynamo() (*dynamo.Item, error) {
	return &dynamo.Item{
		Key:   t.DynamoKey(),
		OrgID: int(t.OrgID),
		Data:  t.Data,
	}, nil
}

func NewMsgDeletionTag(orgID OrgID, contactUUID core.ContactUUID, msgUUID events.EventUUID, byContact bool, u *User) *EventTag {
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

// the client facing names of the message statuses, as used in history items and published events. These have to
// match what courier writes for the same status changes since clients can't tell which service recorded one.
var msgStatusNames = map[MsgStatus]string{
	MsgStatusWired:     "wired",
	MsgStatusSent:      "sent",
	MsgStatusDelivered: "delivered",
	MsgStatusRead:      "read",
	MsgStatusErrored:   "errored",
	MsgStatusFailed:    "failed",
}

// the client facing reasons for a status change, for the failure reasons that are recorded on the status tag rather
// than as the originating event's unsendable_reason (those are set when the message is created, not when it fails).
var msgStatusReasons = map[MsgFailedReason]string{
	MsgFailedErrorLimit:     "error_limit",
	MsgFailedTooOld:         "too_old",
	MsgFailedChannelRemoved: "channel_removed",
}

// NewMsgStatusTag creates the history-table event tag that records an outgoing message's status change. Like the
// airtime equivalent it's keyed by the same UUID as the message's msg_created event and shares a sort key across
// changes, so the latest overwrites, allowing clients to inject the current _status when rendering that event.
// failedReason may be NilMsgFailedReason, and only the reasons in msgStatusReasons appear on the tag.
func NewMsgStatusTag(orgID OrgID, contactUUID core.ContactUUID, msgUUID events.EventUUID, status MsgStatus, failedReason MsgFailedReason) *EventTag {
	data := map[string]any{"created_on": dates.Now(), "status": msgStatusNames[status]}

	if reason := msgStatusReasons[failedReason]; reason != "" {
		data["reason"] = reason
	}

	return &EventTag{
		OrgID:       orgID,
		ContactUUID: contactUUID,
		EventUUID:   msgUUID,
		Tag:         eventTagStatus,
		Data:        data,
	}
}
