package ivr

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaruka/mailroom/core/ivr"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

// TypeBulkCallHangup is the type of the task
const TypeBulkCallHangup = "bulk_call_hangup"

func init() {
	tasks.RegisterType(TypeBulkCallHangup, func() tasks.Task { return &BulkCallHangupTask{} })
}

type Hangup struct {
	SessionID models.SessionID `json:"session_id"`
	CallID    models.CallID    `json:"call_id"`
}

// BulkCallHangupTask is the payload of the task
type BulkCallHangupTask struct {
	Hangups []*Hangup `json:"hangups"`
}

func (t *BulkCallHangupTask) Type() string {
	return TypeBulkCallHangup
}

// Timeout is the maximum amount of time the task can run for
func (t *BulkCallHangupTask) Timeout() time.Duration {
	return time.Hour
}

func (t *BulkCallHangupTask) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform creates the actual task
func (t *BulkCallHangupTask) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets) error {
	log := slog.With("comp", "ivr_cron_expirer")

	rc := rt.RP.Get()
	defer rc.Close()

	sessionIDs := make([]models.SessionID, 0, 100)
	clogs := make([]*models.ChannelLog, 0, 100)

	for _, hangup := range t.Hangups {
		sessionIDs = append(sessionIDs, hangup.SessionID)

		// load our call
		conn, err := models.GetCallByID(ctx, rt.DB, oa.OrgID(), hangup.CallID)
		if err != nil {
			log.Error("unable to load call", "error", err, "call_id", hangup.CallID)
			continue
		}

		// hang up our call
		clog, err := ivr.HangupCall(ctx, rt, conn)
		if err != nil {
			// log error but carry on with other calls
			log.Error("error hanging up call", "error", err, "call_id", conn.ID())
		}

		if clog != nil {
			clogs = append(clogs, clog)
		}
	}

	// now expire our runs and sessions
	if len(sessionIDs) > 0 {
		if err := models.ExitSessions(ctx, rt.DB, sessionIDs, models.SessionStatusExpired); err != nil {
			return fmt.Errorf("error expiring sessions for expired calls: %w", err)
		}
	}

	if err := models.InsertChannelLogs(ctx, rt, clogs); err != nil {
		return fmt.Errorf("error inserting channel logs: %w", err)
	}

	return nil
}
