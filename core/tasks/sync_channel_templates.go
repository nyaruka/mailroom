package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// TypeSyncChannelTemplates is the type of the task to sync a channel's templates
const TypeSyncChannelTemplates = "sync_channel_templates"

func init() {
	RegisterType(TypeSyncChannelTemplates, func() Task { return &SyncChannelTemplates{} })
}

// SyncChannelTemplates is our task to sync a channel's templates from its provider
type SyncChannelTemplates struct {
	ChannelID models.ChannelID `json:"channel_id"`
}

func (t *SyncChannelTemplates) Type() string {
	return TypeSyncChannelTemplates
}

func (*SyncChannelTemplates) Timeout() time.Duration {
	return 10 * time.Minute
}

func (t *SyncChannelTemplates) WithAssets() models.Refresh {
	return models.RefreshNone
}

// Perform implements tasks.Task
func (t *SyncChannelTemplates) Perform(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, taskID TaskID) error {
	channel := oa.ChannelByID(t.ChannelID)

	// channel has since been deleted or changed to a type without templates
	if channel == nil || !templates.HasService(channel.Type()) {
		return nil
	}

	_, err := templates.SyncChannel(ctx, rt, oa, channel)

	// a failed fetch is recorded on the channel, and a sync already in progress covers this one
	var fetchErr *templates.FetchError
	if err != nil && !errors.As(err, &fetchErr) && !errors.Is(err, templates.ErrSyncInProgress) {
		return fmt.Errorf("error syncing templates for channel #%d: %w", t.ChannelID, err)
	}

	return nil
}
