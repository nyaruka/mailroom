package runner

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/vinovest/sqlx"
)

type ordered interface {
	Order() int
}

func orderedCmp[T ordered](a, b T) int { return cmp.Compare(a.Order(), b.Order()) }

// PreCommitHook is a hook that turns the output from event handlers into database changes applied in a transaction.
type PreCommitHook interface {
	ordered

	Execute(context.Context, *runtime.Runtime, *sqlx.Tx, *models.OrgAssets, map[*Scene][]any) error
}

// PostCommitHook is a hook that runs after the transaction has been committed.
type PostCommitHook interface {
	ordered

	Execute(context.Context, *runtime.Runtime, *models.OrgAssets, map[*Scene][]any) error
}

// ExecutePreCommitHooks executes the pre commit hooks for the given scenes
func ExecutePreCommitHooks(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes []*Scene) error {
	// gather all our hook events together across our sessions
	byHook := make(map[PreCommitHook]map[*Scene][]any)
	for _, s := range scenes {
		for hook, args := range s.preCommits {
			byScene, found := byHook[hook]
			if !found {
				byScene = make(map[*Scene][]any, len(scenes))
				byHook[hook] = byScene
			}
			byScene[s] = args
		}
	}

	// get hooks by their declared order
	hookTypes := slices.SortedStableFunc(maps.Keys(byHook), orderedCmp)

	// and apply them in that order
	for _, hook := range hookTypes {
		if err := hook.Execute(ctx, rt, tx, oa, byHook[hook]); err != nil {
			return fmt.Errorf("error applying scene pre commit hook: %T: %w", hook, err)
		}
	}

	return nil
}

// ExecutePostCommitHooks executes the post commit hooks for the given scenes
func ExecutePostCommitHooks(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes []*Scene) error {
	// gather all our hook events together across our sessions
	byHook := make(map[PostCommitHook]map[*Scene][]any)
	for _, s := range scenes {
		for hook, args := range s.postCommits {
			byScene, found := byHook[hook]
			if !found {
				byScene = make(map[*Scene][]any, len(scenes))
				byHook[hook] = byScene
			}
			byScene[s] = args
		}
	}

	// get hooks by their declared order
	hookTypes := slices.SortedStableFunc(maps.Keys(byHook), orderedCmp)

	// and apply them in that order
	for _, hook := range hookTypes {
		if err := hook.Execute(ctx, rt, oa, byHook[hook]); err != nil {
			return fmt.Errorf("error applying scene post commit hook: %T: %w", hook, err)
		}
	}

	return nil
}
