package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeLLMCalled, handleLLMCalled)
}

func handleLLMCalled(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e flows.Event, userID models.UserID) error {
	event := e.(*events.LLMCalled)

	slog.Debug("LLM called", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), slog.Group("llm", "uuid", event.LLM.UUID, "name", event.LLM.Name), "elapsed_ms", event.ElapsedMS)

	llm := oa.SessionAssets().LLMs().Get(event.LLM.UUID)
	if llm != nil {
		m := llm.Asset().(*models.LLM)
		scene.AttachPreCommitHook(hooks.InsertLLMDailyCounts, m.RecordCall(rt, oa, event))
	}

	return nil
}
