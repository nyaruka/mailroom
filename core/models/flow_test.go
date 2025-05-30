package models_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/goflow"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestLoadFlows(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetAll)

	rt.DB.MustExec(`UPDATE flows_flow SET ivr_retry = 30 WHERE id = $1`, testdb.IVRFlow.ID)
	rt.DB.MustExec(`UPDATE flows_flow SET expires_after_minutes = 720 WHERE id = $1`, testdb.Favorites.ID)
	rt.DB.MustExec(`UPDATE flows_flow SET expires_after_minutes = 1 WHERE id = $1`, testdb.PickANumber.ID)          // too small for messaging
	rt.DB.MustExec(`UPDATE flows_flow SET expires_after_minutes = 12345678 WHERE id = $1`, testdb.SingleMessage.ID) // too large for messaging

	sixtyMinutes := 60 * time.Minute
	thirtyMinutes := 30 * time.Minute

	type testcase struct {
		org                *testdb.Org
		id                 models.FlowID
		uuid               assets.FlowUUID
		name               string
		expectedType       models.FlowType
		expectedEngineType flows.FlowType
		expectedExpire     time.Duration
		expectedIVRRetry   *time.Duration
	}

	tcs := []testcase{
		{
			testdb.Org1,
			testdb.Favorites.ID,
			testdb.Favorites.UUID,
			"Favorites",
			models.FlowTypeMessaging,
			flows.FlowTypeMessaging,
			720 * time.Minute,
			&sixtyMinutes, // uses default
		},
		{
			testdb.Org1,
			testdb.PickANumber.ID,
			testdb.PickANumber.UUID,
			"Pick a Number",
			models.FlowTypeMessaging,
			flows.FlowTypeMessaging,
			5 * time.Minute, // clamped to minimum
			&sixtyMinutes,   // uses default
		},
		{
			testdb.Org1,
			testdb.SingleMessage.ID,
			testdb.SingleMessage.UUID,
			"Send All",
			models.FlowTypeMessaging,
			flows.FlowTypeMessaging,
			20160 * time.Minute, // clamped to maximum
			&sixtyMinutes,       // uses default
		},
		{
			testdb.Org1,
			testdb.IVRFlow.ID,
			testdb.IVRFlow.UUID,
			"IVR Flow",
			models.FlowTypeVoice,
			flows.FlowTypeVoice,
			5 * time.Minute,
			&thirtyMinutes, // uses explicit
		},
	}

	assertFlow := func(tc *testcase, dbFlow *models.Flow) {
		desc := fmt.Sprintf("flow id=%d uuid=%s name=%s", tc.id, tc.uuid, tc.name)

		// check properties of flow model
		assert.Equal(t, tc.id, dbFlow.ID())
		assert.Equal(t, tc.uuid, dbFlow.UUID())
		assert.Equal(t, tc.name, dbFlow.Name(), "db name mismatch for %s", desc)
		assert.Equal(t, tc.expectedIVRRetry, dbFlow.IVRRetryWait(), "db IVR retry mismatch for %s", desc)

		// load as engine flow and check that too
		flow, err := goflow.ReadFlow(rt.Config, dbFlow.Definition())
		assert.NoError(t, err, "read flow failed for %s", desc)

		assert.Equal(t, tc.uuid, flow.UUID(), "engine UUID mismatch for %s", desc)
		assert.Equal(t, tc.name, flow.Name(), "engine name mismatch for %s", desc)
		assert.Equal(t, tc.expectedEngineType, flow.Type(), "engine type mismatch for %s", desc)
		assert.Equal(t, tc.expectedExpire, flow.ExpireAfter(), "engine expire mismatch for %s", desc)

	}

	for _, tc := range tcs {
		// test loading by UUID
		dbFlow, err := models.LoadFlowByUUID(ctx, rt.DB.DB, tc.org.ID, tc.uuid)
		assert.NoError(t, err)
		assertFlow(&tc, dbFlow)

		// test loading by name
		dbFlow, err = models.LoadFlowByName(ctx, rt.DB.DB, tc.org.ID, tc.name)
		assert.NoError(t, err)
		assertFlow(&tc, dbFlow)

		// test loading by ID
		dbFlow, err = models.LoadFlowByID(ctx, rt.DB.DB, tc.org.ID, tc.id)
		assert.NoError(t, err)
		assertFlow(&tc, dbFlow)
	}

	// test loading flow with wrong org
	dbFlow, err := models.LoadFlowByID(ctx, rt.DB.DB, testdb.Org2.ID, testdb.Favorites.ID)
	assert.NoError(t, err)
	assert.Nil(t, dbFlow)
}

func TestFlowIDForUUID(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	org, _ := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)

	tx, err := rt.DB.BeginTxx(ctx, nil)
	assert.NoError(t, err)

	id, err := models.FlowIDForUUID(ctx, tx, org, testdb.Favorites.UUID)
	assert.NoError(t, err)
	assert.Equal(t, testdb.Favorites.ID, id)

	// make favorite inactive
	tx.MustExec(`UPDATE flows_flow SET is_active = FALSE WHERE id = $1`, testdb.Favorites.ID)
	tx.Commit()

	defer rt.DB.MustExec(`UPDATE flows_flow SET is_active = TRUE WHERE id = $1`, testdb.Favorites.ID)

	tx, err = rt.DB.BeginTxx(ctx, nil)
	assert.NoError(t, err)
	defer tx.Rollback()

	// clear our assets so it isn't cached
	models.FlushCache()
	org, _ = models.GetOrgAssets(ctx, rt, testdb.Org1.ID)

	id, err = models.FlowIDForUUID(ctx, tx, org, testdb.Favorites.UUID)
	assert.NoError(t, err)
	assert.Equal(t, testdb.Favorites.ID, id)
}
