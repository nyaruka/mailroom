package hooks

import (
	"context"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/msgio"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

// SendMessages is our hook for sending scene messages
var SendMessages runner.PostCommitHook = &sendMessages{}

type sendMessages struct{}

func (h *sendMessages) Order() int { return 1 }

func (h *sendMessages) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	sends := make([]*models.Send, 0, len(scenes))

	// for each scene gather all our messages
	for _, args := range scenes {
		sceneSends := make([]*models.Send, 0, 1)

		for _, m := range args {
			sceneSends = append(sceneSends, &models.Send{Msg: m.(*models.Msg)})
		}

		// mark the last message in the sprint (used for setting timeouts)
		sceneSends[len(sceneSends)-1].LastInSprint = true

		sends = append(sends, sceneSends...)
	}

	msgio.QueueMessages(ctx, rt, sends)
	return nil
}
