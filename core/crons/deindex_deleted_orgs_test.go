package crons_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/crons"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeindexDeletedOrgsCron(t *testing.T) {
	ctx, rt := testsuite.Runtime()
	vc := rt.VK.Get()
	defer vc.Close()

	defer testsuite.Reset(t, testsuite.ResetElastic|testsuite.ResetValkey)

	cron := &crons.DeindexDeletedOrgsCron{}

	assertRun := func(expected map[string]any) {
		res, err := cron.Run(ctx, rt)
		assert.NoError(t, err)
		assert.Equal(t, expected, res)

		_, err = rt.ES.Indices.Refresh().Index(rt.Config.ElasticContactsIndex).Do(ctx)
		require.NoError(t, err)
	}

	testsuite.ReindexElastic(t)

	// no orgs to deindex
	assertRun(map[string]any{"contacts": map[models.OrgID]int{}})

	err := crons.MarkOrgForDeindexing(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	assertRun(map[string]any{"contacts": map[models.OrgID]int{1: 124}})

	// this run finds no contacts to deindex for org 1 and removes it from the set
	assertRun(map[string]any{"contacts": map[models.OrgID]int{1: 0}})

	assertRun(map[string]any{"contacts": map[models.OrgID]int{}})
}
