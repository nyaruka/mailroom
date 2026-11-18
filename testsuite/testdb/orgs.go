package testdb

import (
	"context"
	"testing"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/stretchr/testify/require"
)

type Org struct {
	ID   models.OrgID
	UUID uuids.UUID
}

func (o *Org) Load(t *testing.T, rt *runtime.Runtime) *models.OrgAssets {
	oa, err := models.GetOrgAssets(context.Background(), rt, o.ID)
	require.NoError(t, err)
	return oa
}

// SetStatus sets the status of the org, with the restriction and suspension fields the database requires for that
// status (using a staff reason for non-active statuses), and clears the org assets cache so the change is seen
func (o *Org) SetStatus(t *testing.T, rt *runtime.Runtime, status models.OrgStatus) {
	_, err := rt.DB.Exec(`
	UPDATE orgs_org SET
		status = $2::text,
		status_reason = CASE WHEN $2::text = 'A' THEN NULL ELSE 'S' END,
		status_changed_on = NOW(),
		restricted_reason = CASE WHEN $2::text = 'R' THEN 'S' END,
		suspends_on = CASE WHEN $2::text = 'R' THEN NOW() + INTERVAL '14 days' END,
		suspended_on = CASE WHEN $2::text = 'S' THEN NOW() END
	WHERE id = $1`, o.ID, status)
	require.NoError(t, err)

	models.FlushCache()
}

type User struct {
	ID   models.UserID
	UUID assets.UserUUID
}

func (u *User) SafeID() models.UserID {
	if u != nil {
		return u.ID
	}
	return models.NilUserID
}
