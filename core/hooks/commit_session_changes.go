package hooks

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/null/v3"
)

// CommitSessionChangesHook is our hook for name changes
var CommitSessionChangesHook models.EventCommitHook = &commitSessionChangesHook{}

type commitSessionChangesHook struct{}

// Apply commits our contact current_flow changes as a bulk update for the passed in map of scene
func (h *commitSessionChangesHook) Apply(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*models.Scene][]any) error {
	updates := make([]CurrentSessionUpdate, 0, len(scenes))
	for _, evts := range scenes {
		// there is only ever one of these events per scene
		update := evts[len(evts)-1].(CurrentSessionUpdate)
		updates = append(updates, update)
	}

	// do our update
	return models.BulkQuery(ctx, "updating contact current session", tx, sqlUpdateContactCurrentSession, updates)
}

// struct used for our bulk insert
type CurrentSessionUpdate struct {
	ID                 models.ContactID `db:"id"`
	CurrentSessionUUID null.String      `db:"current_session_uuid"`
	CurrentFlowID      models.FlowID    `db:"current_flow_id"`
}

const sqlUpdateContactCurrentSession = `
UPDATE contacts_contact c
   SET current_session_uuid = r.current_session_uuid::uuid, current_flow_id = r.current_flow_id::int
  FROM (VALUES(:id, :current_session_uuid, :current_flow_id)) AS r(id, current_session_uuid, current_flow_id)
 WHERE c.id = r.id::int`
