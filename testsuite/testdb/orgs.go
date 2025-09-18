package testdb

import (
	"context"
	"testing"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
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
