package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

var QueueContactTask runner.PostCommitHook = &queueContactTask{}

type queueContactTask struct{}

func (h *queueContactTask) Order() int { return 10 }

func (h *queueContactTask) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	for s, args := range scenes {
		for _, arg := range args {
			task := arg.(ctasks.Task)

			if err := tasks.QueueContact(ctx, rt, oa.OrgID(), s.ContactID(), task); err != nil {
				return fmt.Errorf("error queueing contact task: %w", err)
			}
		}
	}

	return nil
}
