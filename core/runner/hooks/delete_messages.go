package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// DeleteMessages is our hook for committing message deletions
var DeleteMessages runner.PreCommitHook = &deleteMessages{}

type deleteMessages struct{}

func (h *deleteMessages) Order() int { return 10 }

func (h *deleteMessages) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	byContact := make([]flows.EventUUID, 0, len(scenes))
	byUser := make([]flows.EventUUID, 0, len(scenes))
	tags := make([]*models.EventTag, 0, len(scenes))

	for s, args := range scenes {
		for _, a := range args {
			del := a.(*MessageDeletion)
			if del.ByContact {
				byContact = append(byContact, del.MsgUUID)
				tags = append(tags, models.NewMsgDeletionTag(oa.OrgID(), s.ContactUUID(), del.MsgUUID, true, nil))
			} else {
				byUser = append(byUser, del.MsgUUID)
				tags = append(tags, models.NewMsgDeletionTag(oa.OrgID(), s.ContactUUID(), del.MsgUUID, false, oa.UserByID(del.UserID)))
			}
		}
	}

	if len(byContact) > 0 {
		if err := models.DeleteMessages(ctx, tx, oa.OrgID(), byContact, models.VisibilityDeletedBySender); err != nil {
			return fmt.Errorf("error deleting messages: %w", err)
		}
	}
	if len(byUser) > 0 {
		if err := models.DeleteMessages(ctx, tx, oa.OrgID(), byUser, models.VisibilityDeletedByUser); err != nil {
			return fmt.Errorf("error deleting messages: %w", err)
		}
	}

	for _, tag := range tags {
		if _, err := rt.Writers.History.Queue(tag); err != nil {
			return fmt.Errorf("error queuing deletion tag to writer: %w", err)
		}
	}

	return nil
}

type MessageDeletion struct {
	MsgUUID   flows.EventUUID
	ByContact bool
	UserID    models.UserID
}
