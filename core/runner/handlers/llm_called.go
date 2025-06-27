package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
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
		m.RecordCall(rt, time.Duration(event.ElapsedMS)*time.Millisecond, event.TokensUsed)
	}

	return nil
}
