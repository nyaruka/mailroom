package search

import (
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
)

// MessageDoc represents a message document in the OpenSearch messages index
type MessageDoc struct {
	Timestamp   time.Time         `json:"@timestamp"`
	OrgID       models.OrgID      `json:"org_id"`
	UUID        flows.EventUUID   `json:"uuid"`
	ContactUUID flows.ContactUUID `json:"contact_uuid"`
	Text        string            `json:"text"`
}
