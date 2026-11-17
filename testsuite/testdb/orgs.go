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

// SetStatus sets the status of the org, keeping the legacy suspended flag consistent with it, and clears the org
// assets cache so the change is seen
func (o *Org) SetStatus(t *testing.T, rt *runtime.Runtime, status models.OrgStatus) {
	suspended := status == models.OrgStatusSuspended
	_, err := rt.DB.Exec(`UPDATE orgs_org SET status = $2, is_suspended = $3, suspended_on = CASE WHEN $3 THEN NOW() END WHERE id = $1`, o.ID, status, suspended)
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
