package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocations(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer rt.DB.MustExec(`DELETE FROM locations_boundaryalias WHERE created_by_id = 2`)

	rt.DB.MustExec(`INSERT INTO locations_boundaryalias(is_active, created_on, modified_on, name, boundary_id, created_by_id, modified_by_id, org_id)
											  VALUES(TRUE, NOW(), NOW(), 'Soko', 8148, 2, 1, 1);`)
	rt.DB.MustExec(`INSERT INTO locations_boundaryalias(is_active, created_on, modified_on, name, boundary_id, created_by_id, modified_by_id, org_id)
	                                          VALUES(TRUE, NOW(), NOW(), 'Sokoz', 8148, 2, 1, 2);`)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshLocations)
	require.NoError(t, err)

	root, err := oa.Locations()
	require.NoError(t, err)

	locations := root[0].FindByName(oa.Env(), "Nigeria", 0, nil)

	assert.Equal(t, 1, len(locations))
	assert.Equal(t, "Nigeria", locations[0].Name())
	assert.Equal(t, []string(nil), locations[0].Aliases())
	assert.Equal(t, 37, len(locations[0].Children()))
	nigeria := locations[0]

	tcs := []struct {
		Name        string
		Level       envs.LocationLevel
		Aliases     []string
		NumChildren int
	}{
		{"Sokoto", 1, []string{"Soko"}, 23},
		{"Zamfara", 1, nil, 14},
	}

	for _, tc := range tcs {
		locations = root[0].FindByName(oa.Env(), tc.Name, tc.Level, nigeria)
		assert.Equal(t, 1, len(locations))
		state := locations[0]

		assert.Equal(t, tc.Name, state.Name())
		assert.Equal(t, tc.Aliases, state.Aliases())
		assert.Equal(t, tc.NumChildren, len(state.Children()))
	}
}
