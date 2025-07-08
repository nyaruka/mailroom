package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

// Task is the interface for all contact tasks - tasks which operate on a single contact in real time
type Task interface {
	Type() string
	UseReadOnly() bool
	Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contact *models.Contact) error
}

var registeredTypes = map[string]func() Task{}

func RegisterContactTask(name string, initFunc func() Task) {
	registeredTypes[name] = initFunc
}

func readTask(type_ string, data []byte) (Task, error) {
	fn := registeredTypes[type_]
	if fn == nil {
		return nil, fmt.Errorf("unknown task type: %s", type_)
	}

	t := fn()
	return t, json.Unmarshal(data, t)
}

// wrapper for encoding a handler task
type payload struct {
	Type       string          `json:"type"`
	Task       json.RawMessage `json:"task"`
	QueuedOn   time.Time       `json:"queued_on"`
	ErrorCount int             `json:"error_count,omitempty"`
}

// QueueTask queues a handler task for the given contact
func QueueTask(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactID models.ContactID, task Task) error {
	return queueTask(ctx, rt, orgID, contactID, task, false, 0)
}

func queueTask(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, contactID models.ContactID, task Task, front bool, errorCount int) error {
	vc := rt.VK.Get()
	defer vc.Close()

	taskJSON, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("error marshalling handler task: %w", err)
	}

	payload := &payload{Type: task.Type(), Task: taskJSON, QueuedOn: dates.Now(), ErrorCount: errorCount}
	payloadJSON := jsonx.MustMarshal(payload)

	// first push the event on our contact queue
	contactQ := fmt.Sprintf("c:%d:%d", orgID, contactID)
	if front {
		_, err = redis.Int64(redis.DoContext(vc, ctx, "LPUSH", contactQ, string(payloadJSON)))

	} else {
		_, err = redis.Int64(redis.DoContext(vc, ctx, "RPUSH", contactQ, string(payloadJSON)))
	}
	if err != nil {
		return fmt.Errorf("error queuing handler task: %w", err)
	}

	// then add a handle task for that contact on our global handler queue to
	err = tasks.Queue(ctx, rt, rt.Queues.Handler, orgID, &HandleContactEventTask{ContactID: contactID}, false)
	if err != nil {
		return fmt.Errorf("error queuing handle task: %w", err)
	}
	return nil
}
