package android

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/mailroom/v26/core/android"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/android/status", web.JSONPayload(handleStatus))
}

// Applies message status changes reported by an Android relayer during a sync.
//
//	{
//	  "org_id": 1,
//	  "channel_id": 12,
//	  "commands": [
//	    {"msg_id": 12345, "cmd": "mt_sent", "ts": "2021-01-01T12:00:00Z"},
//	    {"msg_id": 12346, "cmd": "mt_dlvd", "ts": "2021-01-01T12:00:05Z"}
//	  ]
//	}
//
// The response says whether each command was handled, in the order they were given, so that the caller can tell the
// relayer which ones to stop resending:
//
//	{
//	  "handled": [true, false]
//	}
//
// Commands are identified by message id rather than UUID like every other message endpoint because a relayer only
// ever knows the ids it was given in its sync payload, and resolving those to UUIDs first would mean an extra query
// that still has to preserve which ids didn't resolve.
type statusRequest struct {
	OrgID     models.OrgID     `json:"org_id"     validate:"required"`
	ChannelID models.ChannelID `json:"channel_id" validate:"required"`
	Commands  []*statusCommand `json:"commands"   validate:"required,dive"`
}

type statusCommand struct {
	MsgID models.MsgID `json:"msg_id" validate:"required"`
	Cmd   string       `json:"cmd"    validate:"required"`
	Ts    time.Time    `json:"ts"     validate:"required"`
}

func handleStatus(ctx context.Context, rt *runtime.Runtime, r *statusRequest) (any, int, error) {
	msgIDs := make([]models.MsgID, len(r.Commands))
	for i, c := range r.Commands {
		msgIDs[i] = c.MsgID
	}

	refs, err := android.GetMessageRefs(ctx, rt, r.OrgID, r.ChannelID, msgIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("error resolving messages to update: %w", err)
	}

	// whether each command was applied, so that the caller can tell the relayer which ones to stop resending
	handled := make([]bool, len(r.Commands))
	batch := &android.StatusBatch{}

	for i, c := range r.Commands {
		handled[i] = batch.Add(refs[c.MsgID], c.Cmd, c.Ts)
	}

	if err := batch.Apply(ctx, rt, r.OrgID); err != nil {
		return nil, 0, err
	}

	return map[string]any{"handled": handled}, http.StatusOK, nil
}
