package handlers

import (
	"context"
	"log/slog"

	"github.com/nyaruka/gocommon/tallies"
	"github.com/nyaruka/goflow/core/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/runner/hooks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	runner.RegisterEventHandler(events.TypeEmailSent, handleEmailSent)
}

// goflow sends the email so this just logs the event and tallies sends using the shared email configuration
func handleEmailSent(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scene *runner.Scene, e events.Event, userID models.UserID) error {
	event := e.(*events.EmailSent)

	slog.Debug("email sent", "contact", scene.ContactUUID(), "session", scene.SessionUUID(), "subject", event.Subject, "body", event.Body)

	_, shared, err := oa.Org().EmailSMTP(ctx, rt)
	if err != nil {
		slog.Error("error getting email configuration for tallying", "error", err, "org_id", oa.OrgID())
	} else if shared {
		key := tallies.Daily(models.TallyEmailsShared, event.CreatedOn(), models.TallyEmailsSharedTTL)
		scene.AttachPostCommitHook(hooks.IncrementTallies, map[tallies.Key]int64{key: int64(len(event.To))})
	}

	return nil
}
