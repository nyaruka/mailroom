package runner

import (
	"context"
	"testing"

	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/assert"
)

func TestWrapEventHandler(t *testing.T) {
	calls := []string{}

	RegisterEventHandler("test_wrap", func(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, s *Scene, e events.Event, userID models.UserID) error {
		calls = append(calls, "base")
		return nil
	})
	defer delete(eventHandlers, "test_wrap")

	WrapEventHandler("test_wrap", func(base EventHandler) EventHandler {
		return func(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, s *Scene, e events.Event, userID models.UserID) error {
			calls = append(calls, "wrapper")
			return base(ctx, rt, oa, s, e, userID)
		}
	})

	err := eventHandlers["test_wrap"](context.Background(), nil, nil, nil, nil, models.NilUserID)
	assert.NoError(t, err)
	assert.Equal(t, []string{"wrapper", "base"}, calls)

	// wrapping a type with no registered handler is a bug
	assert.Panics(t, func() {
		WrapEventHandler("test_unregistered", func(base EventHandler) EventHandler { return base })
	})
}
