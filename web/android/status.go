package android

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/status", web.JSONPayload(handleStatus))
}

// Applies message status changes reported by an Android relayer during a sync.
//
//	{
//	  "org_id": 1,
//	  "commands": [
//	    {"msg_id": 12345, "cmd": "mt_sent", "ts": "2021-01-01T12:00:00Z"},
//	    {"msg_id": 12346, "cmd": "mt_dlvd", "ts": "2021-01-01T12:00:05Z"}
//	  ]
//	}
//
// The response says whether each command was handled, in the order they were given, so that the caller can tell the
// relayer which ones to stop resending:
//
//	{
//	  "handled": [true, false]
//	}
//
// Commands are identified by message id rather than UUID like every other message endpoint because a relayer only
// ever knows the ids it was given in its sync payload, and resolving those to UUIDs first would mean an extra query
// that still has to preserve which ids didn't resolve.
type statusRequest struct {
	OrgID     models.OrgID     `json:"org_id"     validate:"required"`
	ChannelID models.ChannelID `json:"channel_id" validate:"required"`
	Commands  []*statusCommand `json:"commands"   validate:"required,dive"`
}

type statusCommand struct {
	MsgID models.MsgID `json:"msg_id" validate:"required"`
	Cmd   string       `json:"cmd"    validate:"required"`
	Ts    time.Time    `json:"ts"     validate:"required"`
}

// the message status commands a relayer can send, and the status each one puts the message into
var statusCommands = map[string]models.MsgStatus{
	"mt_error": models.MsgStatusErrored,
	"mt_fail":  models.MsgStatusFailed,
	"mt_sent":  models.MsgStatusSent,
	"mt_dlvd":  models.MsgStatusDelivered,
}

func handleStatus(ctx context.Context, rt *runtime.Runtime, r *statusRequest) (any, int, error) {
	msgIDs := make([]models.MsgID, len(r.Commands))
	for i, c := range r.Commands {
		msgIDs[i] = c.MsgID
	}

	refs, err := getMessageRefs(ctx, rt, r.OrgID, r.ChannelID, msgIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error resolving messages to update: %w", err)
	}

	// whether each command was applied, so that the caller can tell the relayer which ones to stop resending
	handled := make([]bool, len(r.Commands))
	batch := &statusBatch{}

	for i, c := range r.Commands {
		handled[i] = batch.add(refs[c.MsgID], c.Cmd, c.Ts)
	}

	if err := batch.apply(ctx, rt, r.OrgID); err != nil {
		return nil, 0, err
	}

	return map[string]any{"handled": handled}, http.StatusOK, nil
}

// statusBatch accumulates the status changes a relayer reports across one sync. A sync can report more than one
// change for the same message, e.g. sent and then delivered, so they're folded into a single update per message -
// they're applied in one statement, and replaying them sequentially is what preserves the sent_on semantics.
type statusBatch struct {
	updates   []*models.AndroidStatusUpdate
	byMsgUUID map[events.EventUUID]*models.AndroidStatusUpdate
}

// add records a status command for the given message, returning whether the relayer should consider it handled. A
// nil ref means we have no such message, and an unrecognized command leaves the message alone - in both cases the
// relayer keeps the command and reports it again on its next sync.
func (b *statusBatch) add(ref *msgRef, cmd string, ts time.Time) bool {
	if ref == nil {
		return false
	}

	// incoming messages have no status for a relayer to report but it shouldn't keep resending the command
	if ref.Direction == models.DirectionIn {
		return true
	}

	status, exists := statusCommands[cmd]
	if !exists {
		return false
	}

	u := b.byMsgUUID[ref.UUID]
	if u == nil {
		u = &models.AndroidStatusUpdate{MsgUUID: ref.UUID}
		b.updates = append(b.updates, u)
		if b.byMsgUUID == nil {
			b.byMsgUUID = make(map[events.EventUUID]*models.AndroidStatusUpdate)
		}
		b.byMsgUUID[ref.UUID] = u
	}

	u.Status = status

	switch cmd {
	case "mt_sent":
		// this is the definitive report of when the message left the phone
		u.SentOn = &ts
		u.OverwriteSentOn = true
	case "mt_dlvd":
		// delivery only tells us when the message arrived, so it stands in for sent_on only if no sent report ever
		// arrived - either earlier in this sync or on the message already
		if u.SentOn == nil {
			u.SentOn = &ts
		}
	}

	return true
}

// apply writes the accumulated changes and records each one in the contact's history so that clients rendering the
// message see its new status.
func (b *statusBatch) apply(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID) error {
	tags, err := models.UpdateAndroidMessageStatuses(ctx, rt.DB, orgID, b.updates)
	if err != nil {
		return fmt.Errorf("error updating message statuses: %w", err)
	}

	for _, tag := range tags {
		if _, err := rt.Dynamo.History.Queue(tag); err != nil {
			return fmt.Errorf("error queuing status tag to writer: %w", err)
		}
	}

	return nil
}

type msgRef struct {
	ID        models.MsgID     `db:"id"`
	UUID      events.EventUUID `db:"uuid"`
	Direction models.Direction `db:"direction"`
}

// resolves the ids a relayer knows its messages by to the UUIDs everything else keys messages by - along with the
// direction, because the caller has to tell a message it should ignore from one that doesn't exist.
//
// Scoped to the channel rather than just the workspace: a relayer can only have been given messages on its own
// channel, so anything else it names is either a mistake or an id it has mangled, and either way isn't something we
// want it deciding the status of.
func getMessageRefs(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, channelID models.ChannelID, msgIDs []models.MsgID) (map[models.MsgID]*msgRef, error) {
	rows := []*msgRef{}
	err := rt.DB.SelectContext(ctx, &rows, `SELECT id, uuid, direction FROM msgs_msg WHERE org_id = $1 AND channel_id = $2 AND id = ANY($3)`, orgID, channelID, pq.Array(msgIDs))
	if err != nil {
		return nil, err
	}

	refs := make(map[models.MsgID]*msgRef, len(rows))
	for _, r := range rows {
		refs[r.ID] = r
	}
	return refs, nil
}
