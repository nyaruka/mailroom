package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/templates"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	handler := web.WriteDeadline(syncTemplatesWriteTimeout, web.JSONPayload(handleSyncTemplates))

	web.InternalRoute(http.MethodPost, "/channel/sync_templates", handler)
}

// a sync can make a lot of provider calls, so it gets longer than the server's write timeout, but it stays under what
// proxies between the caller and us typically allow
const syncTemplatesWriteTimeout = 55 * time.Second

// SyncTemplatesTimeout is how long the sync itself gets, which stays under the write timeout so that running out of
// time is reported as a proper error response
var SyncTemplatesTimeout = 50 * time.Second

// Syncs a channel's templates from its provider, returning how many translations it now has.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 235
//	}
type syncTemplatesRequest struct {
	OrgID     models.OrgID     `json:"org_id"     validate:"required"`
	ChannelID models.ChannelID `json:"channel_id" validate:"required"`
}

//	{
//	  "translations": 12
//	}
type syncTemplatesResponse struct {
	Translations int `json:"translations"`
}

func handleSyncTemplates(ctx context.Context, rt *runtime.Runtime, r *syncTemplatesRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	channel := oa.ChannelByID(r.ChannelID)
	if channel == nil {
		return errors.New("no such channel"), http.StatusBadRequest, nil
	}
	if !templates.HasService(channel.Type()) {
		return errors.New("channel type doesn't support templates"), http.StatusBadRequest, nil
	}

	ctx, cancel := context.WithTimeout(ctx, SyncTemplatesTimeout)
	defer cancel()

	num, err := templates.SyncChannel(ctx, rt, channel)
	if err != nil {
		var fetchErr *templates.FetchError
		if errors.As(err, &fetchErr) {
			return err, http.StatusUnprocessableEntity, nil
		} else if errors.Is(err, templates.ErrSyncInProgress) {
			return err, http.StatusConflict, nil
		}
		return nil, 0, fmt.Errorf("error syncing templates: %w", err)
	}

	return &syncTemplatesResponse{Translations: num}, http.StatusOK, nil
}
