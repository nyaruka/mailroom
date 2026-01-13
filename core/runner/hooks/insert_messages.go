package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

// InsertMessages is our hook for committing scene messages
var InsertMessages runner.PreCommitHook = &insertMessages{}

type insertMessages struct{}

func (h *insertMessages) Order() int { return 20 } // after our urn creation hook

func (h *insertMessages) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	msgs := make([]*models.Msg, 0, len(scenes))
	for scene, args := range scenes {
		for _, m := range args {
			msg, urn := m.(MsgAndURN).Msg, m.(MsgAndURN).URN

			// if a URN was added during the flow sprint, message won't have an URN ID which we need to insert it
			if msg.ContactURNID() == models.NilURNID && urn != urns.NilURN {
				cu, err := models.CreateOrClaimURN(ctx, tx, oa, scene.ContactID(), urn)
				if err != nil {
					return fmt.Errorf("error creating new URN %s for message: %w", urn, err)
				}
				msg.SetContactURNID(cu.ID)
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
