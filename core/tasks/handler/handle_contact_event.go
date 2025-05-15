package handler

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dbutil"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/starts"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeHandleContactEvent is the task type for flagging that a contact has handler tasks to be handled
const TypeHandleContactEvent = "handle_contact_event"

func init() {
	tasks.RegisterType(TypeHandleContactEvent, func() tasks.Task { return &HandleContactEventTask{} })
}

// HandleContactEventTask is the task to flag that a contact has tasks
type HandleContactEventTask struct {
	ContactID models.ContactID `json:"contact_id"`
}

func (t *HandleContactEventTask) Type() string {
	return TypeHandleContactEvent
}

// Timeout is the maximum amount of time the task can run for
func (t *HandleContactEventTask) Timeout() time.Duration {
	return time.Minute * 5
}

func (t *HandleContactEventTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform is called when an event comes in for a contact. To make sure we don't get into a situation of being off by one,
// this task ingests and handles all the events for a contact, one by one.
func (t *HandleContactEventTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	// try to get the lock for this contact, waiting up to 10 seconds
	locks, _, err := models.LockContacts(ctx, rt, oa.OrgID(), []models.ContactID{t.ContactID}, time.Second*10)
	if err != nil {
		return fmt.Errorf("error acquiring lock for contact %d: %w", t.ContactID, err)
	}

	// we didn't get the lock.. requeue for later
	if len(locks) == 0 {
		rc := rt.RP.Get()
		defer rc.Close()
		err = tasks.Queue(rc, tasks.HandlerQueue, oa.OrgID(), &HandleContactEventTask{ContactID: t.ContactID}, false)
		if err != nil {
			return fmt.Errorf("error re-adding contact task after failing to get lock: %w", err)
		}
		slog.Info("failed to get lock for contact, requeued and skipping", "org_id", oa.OrgID(), "contact_id", t.ContactID)
		return nil
	}

	defer models.UnlockContacts(rt, oa.OrgID(), locks)

	// read all the events for this contact, one by one
	contactQ := fmt.Sprintf("c:%d:%d", oa.OrgID(), t.ContactID)
	for {
		// pop the next event off this contacts queue
		rc := rt.RP.Get()
		event, err := redis.Bytes(rc.Do("LPOP", contactQ))
		rc.Close()

		// out of tasks? that's ok, exit
		if err == redis.ErrNil {
			return nil
		}

		// real error? report
		if err != nil {
			return fmt.Errorf("error popping handler task: %w", err)
		}

		// decode our event, this is a normal task at its top level
		taskPayload := &payload{}
		jsonx.MustUnmarshal([]byte(event), taskPayload)

		ctask, err := readTask(taskPayload.Type, taskPayload.Task)
		if err != nil {
			return fmt.Errorf("error reading handler task: %w", err)
		}

		start := time.Now()
		log := slog.With("org_id", oa.OrgID(), "contact_id", t.ContactID, "type", taskPayload.Type)

		err = performHandlerTask(ctx, rt, oa, t.ContactID, ctask)

		// record metrics
		rt.Stats.RecordHandlerTask(taskPayload.Type, time.Since(start), time.Since(taskPayload.QueuedOn))

		// if we get an error processing an event, requeue it for later and return our error
		if err != nil {
			if qerr := dbutil.AsQueryError(err); qerr != nil {
				query, params := qerr.Query()
				log = log.With("sql", query, "sql_params", params)
			}

			taskPayload.ErrorCount++
			if taskPayload.ErrorCount < 3 {
				rc := rt.RP.Get()
				retryErr := queueTask(rc, oa.OrgID(), t.ContactID, ctask, true, taskPayload.ErrorCount)
				if retryErr != nil {
					log.Error("error requeuing errored contact event", "error", retryErr)
				}
				rc.Close()

				log.Error("error handling contact event", "error", err, "error_count", taskPayload.ErrorCount)
				return nil
			}
			log.Error("error handling contact event, permanent failure", "error", err)
			return nil
		}

		log.Debug("handler task completed", "elapsed", time.Since(start))
	}
}

func performHandlerTask(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, contactID models.ContactID, task Task) error {
	var db models.Queryer = rt.DB
	if task.UseReadOnly() {
		db = rt.ReadonlyDB
	}

	contact, err := models.LoadContact(ctx, db, oa, contactID)
	if err != nil {
		if err == sql.ErrNoRows { // if contact no longer exists, ignore event, whatever it was gonna do is about to be deleted too
			return nil
		}
		return fmt.Errorf("error loading contact: %w", err)
	}

	return task.Perform(ctx, rt, oa, contact)
}

type DBHook func(ctx context.Context, tx *sqlx.Tx) error

// TriggerIVRFlow will create a new flow start with the passed in flow and set of contacts. This will cause us to
// request calls to start, which once we get the callback will trigger our actual flow to start.
func TriggerIVRFlow(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, flowID models.FlowID, contactIDs []models.ContactID, hook DBHook) error {
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting transaction for IVR flow start: %w", err)
	}

	start := models.NewFlowStart(orgID, models.StartTypeTrigger, flowID).WithContactIDs(contactIDs)
	if err := models.InsertFlowStarts(ctx, tx, []*models.FlowStart{start}); err != nil {
		tx.Rollback()
		return fmt.Errorf("error inserting ivr flow start: %w", err)
	}

	// call our hook if we have one
	if hook != nil {
		if err := hook(ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("error while calling db hook: %w", err)
		}
	}

	// commit our transaction
	if err := tx.Commit(); err != nil {
		tx.Rollback()
		return fmt.Errorf("error committing transaction for ivr flow starts: %w", err)
	}

	// create our batch of all our contacts
	task := &starts.StartIVRFlowBatchTask{FlowStartBatch: start.CreateBatch(contactIDs, true, true, len(contactIDs))}

	// queue this to our ivr starter, it will take care of creating the calls then calling back in
	rc := rt.RP.Get()
	defer rc.Close()
	if err := tasks.Queue(rc, tasks.BatchQueue, orgID, task, true); err != nil {
		return fmt.Errorf("error queuing ivr flow start: %w", err)
	}

	return nil
}
