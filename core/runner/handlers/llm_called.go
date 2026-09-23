package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeLLMCalled, handleLLMCalled)
	runner.RegisterEventHandler(events.TypeClassifierCalled, handleClassifierCalled)
}

func handleLLMCalled(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.LLMCalled)

	slog.Debug("LLM called", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), slog.Group("llm", "uuid", event.LLM.UUID, "name", event.LLM.Name), "elapsed_ms", event.ElapsedMS)

	recordLLMCall(rt, oa, scene, event.LLM, event.ElapsedMS, event.Tokens)
	return nil
}

func handleClassifierCalled(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.ClassifierCalled)

	slog.Debug("classifier called", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), slog.Group("llm", "uuid", event.Model.UUID, "name", event.Model.Name), "elapsed_ms", event.ElapsedMS)

	recordLLMCall(rt, oa, scene, event.Model, event.ElapsedMS, event.Tokens)
	return nil
}

func recordLLMCall(rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, ref *assets.LLMReference, elapsedMS int64, tokens events.LLMTokens) {
	llm := oa.SessionAssets().LLMs().Get(ref.UUID)
	if llm != nil {
		m := llm.Asset().(*models.LLM)
		scene.AttachPreCommitHook(hooks.InsertLLMDailyCounts, m.RecordCall(rt, oa, time.Duration(elapsedMS)*time.Millisecond, tokens))
	}
}
