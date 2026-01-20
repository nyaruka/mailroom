package hooks

import (
	"context"
	"fmt"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/ctasks"
	"github.com/nyaruka/mailroom/runtime"
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
