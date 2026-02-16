package testdb

import (
	"testing"

	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/stretchr/testify/require"
)

type Campaign struct {
	ID   models.CampaignID
	UUID assets.CampaignUUID
}

type CampaignPoint struct {
	ID   models.PointID
	UUID assets.CampaignPointUUID
}

func InsertCampaign(t *testing.T, rt *runtime.Runtime, org *Org, name string, group *Group) *Campaign {
	uuid := assets.CampaignUUID(uuids.NewV4())
	var id models.CampaignID
	err := rt.DB.Get(&id,
		`INSERT INTO campaigns_campaign(uuid, org_id, name, group_id, is_archived, is_system, is_active, created_on, modified_on, created_by_id, modified_by_id) 
		VALUES($1, $2, $3, $4, FALSE, FALSE, TRUE, NOW(), NOW(), 1, 1) RETURNING id`, uuid, org.ID, name, group.ID,
	)
	require.NoError(t, err)
	return &Campaign{id, uuid}
}

func InsertCampaignFlowPoint(t *testing.T, rt *runtime.Runtime, campaign *Campaign, flow *Flow, relativeTo *Field, offset int, unit string) *CampaignPoint {
	uuid := assets.CampaignPointUUID(uuids.NewV4())
	var id models.PointID
	err := rt.DB.Get(&id,
		`INSERT INTO campaigns_campaignevent(uuid, campaign_id, event_type, status, fire_version, flow_id, relative_to_id, "offset", unit, delivery_hour, start_mode, is_active, created_on, modified_on, created_by_id, modified_by_id)
		VALUES($1, $2, 'F', 'R', 1, $3, $4, $5, $6, -1, 'I', TRUE, NOW(), NOW(), 1, 1) RETURNING id`,
		uuid, campaign.ID, flow.ID, relativeTo.ID, offset, unit,
	)
	require.NoError(t, err)
	return &CampaignPoint{id, uuid}
}

func InsertCampaignMsgPoint(t *testing.T, rt *runtime.Runtime, campaign *Campaign, flow *Flow, relativeTo *Field, offset int, unit string) *CampaignPoint {
	uuid := assets.CampaignPointUUID(uuids.NewV4())
	var id models.PointID
	err := rt.DB.Get(&id,
		`INSERT INTO campaigns_campaignevent(uuid, campaign_id, event_type, status, fire_version, flow_id, relative_to_id, "offset", unit, delivery_hour, start_mode, is_active, created_on, modified_on, created_by_id, modified_by_id)
		VALUES($1, $2, 'M', 'R', 1, $3, $4, $5, $6, -1, 'I', TRUE, NOW(), NOW(), 1, 1) RETURNING id`,
		uuid, campaign.ID, flow.ID, relativeTo.ID, offset, unit,
	)
	require.NoError(t, err)
	return &CampaignPoint{id, uuid}
}
