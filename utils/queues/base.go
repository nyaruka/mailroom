package queues

import (
	"encoding/json"
	"time"

	"github.com/nyaruka/gocommon/queues"
)

// Task is a wrapper for encoding a task
type Task struct {
	ID       queues.TaskID   `json:"-"`
	OwnerID  int             `json:"-"`
	Attempts int             `json:"-"`
	Type     string          `json:"type"`
	Task     json.RawMessage `json:"task"`
	QueuedOn time.Time       `json:"queued_on"`
}
