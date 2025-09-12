package models_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStarts(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	startID := testdb.InsertFlowStart(rt, testdb.Org1, testdb.Admin, testdb.SingleMessage, []*testdb.Contact{testdb.Ann, testdb.Bob})

	startJSON := fmt.Appendf(nil, `{
		"start_id": %d,
		"start_type": "M",
		"org_id": %d,
		"created_by_id": %d,
		"exclusions": {},
		"flow_id": %d,
		"flow_type": "M",
		"contact_ids": [%d, %d],
		"group_ids": [%d],
		"exclude_group_ids": [%d],
		"urns": ["tel:+12025550199"],
		"query": null,
		"params": {"foo": "bar"},
		"parent_summary": {"uuid": "b65b1a22-db6d-4f5a-9b3d-7302368a82e6"},
		"session_history": {"parent_uuid": "532a3899-492f-4ffe-aed7-e75ad524efab", "ancestors": 3, "ancestors_since_input": 1}
	}`, startID, testdb.Org1.ID, testdb.Admin.ID, testdb.SingleMessage.ID, testdb.Ann.ID, testdb.Bob.ID, testdb.DoctorsGroup.ID, testdb.TestersGroup.ID)

	start := &models.FlowStart{}
	err := json.Unmarshal(startJSON, start)

	require.NoError(t, err)
	assert.Equal(t, startID, start.ID)
	assert.Equal(t, testdb.Org1.ID, start.OrgID)
	assert.Equal(t, testdb.Admin.ID, start.CreatedByID)
	assert.Equal(t, testdb.SingleMessage.ID, start.FlowID)
	assert.Equal(t, "", start.Query)
	assert.False(t, start.Exclusions.StartedPreviously)
	assert.False(t, start.Exclusions.InAFlow)
	assert.Equal(t, []models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, start.ContactIDs)
	assert.Equal(t, []models.GroupID{testdb.DoctorsGroup.ID}, start.GroupIDs)
	assert.Equal(t, []models.GroupID{testdb.TestersGroup.ID}, start.ExcludeGroupIDs)

	assert.Equal(t, json.RawMessage(`{"uuid": "b65b1a22-db6d-4f5a-9b3d-7302368a82e6"}`), start.ParentSummary)
	assert.Equal(t, json.RawMessage(`{"parent_uuid": "532a3899-492f-4ffe-aed7-e75ad524efab", "ancestors": 3, "ancestors_since_input": 1}`), start.SessionHistory)
	assert.Equal(t, json.RawMessage(`{"foo": "bar"}`), start.Params)

	assertdb.Query(t, rt.DB, `SELECT count(*) FROM flows_flowstart_contacts WHERE flowstart_id = $1`, startID).Returns(2)

	err = start.SetQueued(ctx, rt.DB, 5)
	require.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status, contact_count FROM flows_flowstart WHERE id = $1`, startID).Columns(map[string]any{"status": "Q", "contact_count": int64(5)})

	batch := start.CreateBatch([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, true, false, 3)
	assert.Equal(t, startID, batch.StartID)
	assert.Equal(t, []models.ContactID{testdb.Ann.ID, testdb.Bob.ID}, batch.ContactIDs)
	assert.False(t, batch.IsLast)
	assert.Equal(t, 3, batch.TotalContacts)

	history, err := models.ReadSessionHistory(start.SessionHistory)
	assert.NoError(t, err)
	assert.Equal(t, flows.SessionUUID("532a3899-492f-4ffe-aed7-e75ad524efab"), history.ParentUUID)

	_, err = models.ReadSessionHistory([]byte(`{`))
	assert.EqualError(t, err, "unexpected end of JSON input")

	err = start.SetStarted(ctx, rt.DB)
	require.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, startID).Returns("S")

	err = start.SetCompleted(ctx, rt.DB)
	require.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, startID).Returns("C")

	err = start.SetFailed(ctx, rt.DB)
	require.NoError(t, err)
	assertdb.Query(t, rt.DB, `SELECT status FROM flows_flowstart WHERE id = $1`, startID).Returns("F")

	// try fetching a start from the database (won't load all fields)
	start, err = models.GetFlowStartByID(ctx, rt.DB, start.ID)
	assert.NoError(t, err)
	assert.Equal(t, startID, start.ID)
	assert.Equal(t, testdb.Org1.ID, start.OrgID)
	assert.Equal(t, models.StartStatusFailed, start.Status)
	assert.Equal(t, models.StartTypeManual, start.StartType)
	assert.Equal(t, testdb.Admin.ID, start.CreatedByID)
	assert.Equal(t, testdb.SingleMessage.ID, start.FlowID)
}

func TestStartsBuilding(t *testing.T) {
	uuids.SetGenerator(uuids.NewSeededGenerator(12345, time.Now))
	defer uuids.SetGenerator(uuids.DefaultGenerator)

	start := models.NewFlowStart(testdb.Org1.ID, models.StartTypeManual, testdb.Favorites.ID).
		WithGroupIDs([]models.GroupID{testdb.DoctorsGroup.ID}).
		WithExcludeGroupIDs([]models.GroupID{testdb.TestersGroup.ID}).
		WithContactIDs([]models.ContactID{testdb.Ann.ID, testdb.Bob.ID}).
		WithQuery(`language != ""`).
		WithCreateContact(true).
		WithParams([]byte(`{"foo": "bar"}`))

	marshalled, err := jsonx.Marshal(start)
	require.NoError(t, err)

	test.AssertEqualJSON(t, fmt.Appendf(nil, `{
		"contact_ids": [%d, %d],
		"create_contact": true,
		"created_by_id": null,
		"exclude_group_ids": [%d],
		"exclusions": {
			"in_a_flow": false,
        	"non_active": false,
        	"not_seen_since_days": 0,
        	"started_previously": false
		},
		"flow_id": %d,
		"group_ids": [%d],
		"org_id": 1,
		"params": {
			"foo": "bar"
		},
		"query": "language != \"\"",
		"start_id": null,
		"start_type": "M"
	}`, testdb.Ann.ID, testdb.Bob.ID, testdb.TestersGroup.ID, testdb.Favorites.ID, testdb.DoctorsGroup.ID), marshalled)
}
