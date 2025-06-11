package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// InsertMessages is our hook for comitting scene messages
var InsertMessages runner.PreCommitHook = &insertMessages{}

type insertMessages struct{}

func (h *insertMessages) Order() int { return 1 }

func (h *insertMessages) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	msgs := make([]*models.Msg, 0, len(scenes))
	for scene, s := range scenes {
		for _, m := range s {
			msg, urn := m.(MsgAndURN).Msg, m.(MsgAndURN).URN

			// if a URN was added during the flow sprint, message won't have an URN ID which we need to insert it
			if msg.ContactURNID() == models.NilURNID && urn != urns.NilURN {
				urn, err := models.GetOrCreateURN(ctx, tx, oa, scene.ContactID(), urn)
				if err != nil {
					return fmt.Errorf("error to getting URN: %s: %w", urn, err)
				}
				msg.SetURN(urn) // extracts id from param
			}

			msgs = append(msgs, msg)
		}
	}

	if err := models.InsertMessages(ctx, tx, msgs); err != nil {
		return fmt.Errorf("error writing messages: %w", err)
	}

	return nil
}

type MsgAndURN struct {
	Msg *models.Msg
	URN urns.URN
}
