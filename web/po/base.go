package po

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

var excludeProperties = []string{"arguments"}

func loadFlows(ctx context.Context, rt *runtime.Runtime, orgID models.OrgID, flowIDs []models.FlowID) ([]flows.Flow, error) {
	// grab our org assets
	oa, err := models.GetOrgAssets(ctx, rt, orgID)
	if err != nil {
		return nil, fmt.Errorf("error loading org assets: %w", err)
	}

	flows := make([]flows.Flow, len(flowIDs))
	for i, flowID := range flowIDs {
		dbFlow, err := oa.FlowByID(flowID)
		if err != nil {
			return nil, fmt.Errorf("unable to load flow with ID %d: %w", flowID, err)
		}

		flow, err := oa.SessionAssets().Flows().Get(dbFlow.UUID())
		if err != nil {
			return nil, fmt.Errorf("unable to read flow with UUID %s: %w", string(dbFlow.UUID()), err)
		}

		flows[i] = flow
	}
	return flows, nil
}
