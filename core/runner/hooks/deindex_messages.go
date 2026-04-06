package hooks

import (
	"context"
	"log/slog"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/runner"
	"github.com/nyaruka/mailroom/v26/core/search"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// DeindexMessages is our hook for de-indexing deleted messages from Elasticsearch
var DeindexMessages runner.PostCommitHook = &deindexMessages{}

type deindexMessages struct{}

func (h *deindexMessages) Order() int { return 20 }

func (h *deindexMessages) Execute(ctx context.Context, rt *runtime.Runtime, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	msgUUIDs := make([]flows.EventUUID, 0, len(scenes))
	for _, args := range scenes {
		for _, a := range args {
			msgUUIDs = append(msgUUIDs, a.(flows.EventUUID))
		}
	}

	deleted, err := search.DeindexMessages(ctx, rt, oa.OrgID(), msgUUIDs)
	if err != nil {
		slog.Error("error deindexing messages from elasticsearch", "error", err, "org_id", oa.OrgID(), "count", len(msgUUIDs))
		return nil
	}

	slog.Debug("deindexed messages from elasticsearch", "count", deleted)

	return nil
}
