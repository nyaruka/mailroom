package android

import (
	"context"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// the message status commands a relayer can send, and the status each one puts the message into
var statusCommands = map[string]models.MsgStatus{
	"mt_error": models.MsgStatusErrored,
	"mt_fail":  models.MsgStatusFailed,
	"mt_sent":  models.MsgStatusSent,
	"mt_dlvd":  models.MsgStatusDelivered,
}

// StatusBatch accumulates the status changes a relayer reports across one sync. A sync can report more than one
// change for the same message, e.g. sent and then delivered, so they're folded into a single update per message -
// they're applied in one statement, and replaying them sequentially is what preserves the sent_on semantics.
type StatusBatch struct {
	updates   []*models.AndroidStatusUpdate
	byMsgUUID map[events.EventUUID]*models.AndroidStatusUpdate
}

// Add records a status command for the given message, returning whether the relayer should consider it handled. A
// nil ref means we have no such message, and an unrecognized command leaves the message alone - in both cases the
// relayer keeps the command and reports it again on its next sync.
func (b *StatusBatch) Add(ref *MsgRef, cmd string, ts time.Time) bool {
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

// Apply writes the accumulated changes and records each one in the contact's history so that clients rendering the
// message see its new status.
func (b *StatusBatch) Apply(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID) error {
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

// MsgRef is what a relayer-reported message id resolves to: the UUID everything else keys messages by, and the
// direction, because callers have to tell a message they should ignore from one that doesn't exist.
type MsgRef struct {
	ID        models.MsgID     `db:"id"`
	UUID      events.EventUUID `db:"uuid"`
	Direction models.Direction `db:"direction"`
}

// GetMessageRefs resolves the ids a relayer knows its messages by.
//
// Scoped to the channel rather than just the workspace: a relayer can only have been given messages on its own
// channel, so anything else it names is either a mistake or an id it has mangled, and either way isn't something we
// want it deciding the status of.
func GetMessageRefs(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, channelID models.ChannelID, msgIDs []models.MsgID) (map[models.MsgID]*MsgRef, error) {
	rows := []*MsgRef{}
	err := rt.DB.SelectContext(ctx, &rows, `SELECT id, uuid, direction FROM msgs_msg WHERE org_id = $1 AND channel_id = $2 AND id = ANY($3)`, orgID, channelID, pq.Array(msgIDs))
	if err != nil {
		return nil, err
	}

	refs := make(map[models.MsgID]*MsgRef, len(rows))
	for _, r := range rows {
		refs[r.ID] = r
	}
	return refs, nil
}
