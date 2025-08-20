package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
)

type WebhookCall struct {
	NodeUUID flows.NodeUUID
	Event    *events.WebhookCalled
}

var MonitorWebhooks runner.PreCommitHook = &monitorWebhooks{}

type monitorWebhooks struct{}

func (h *monitorWebhooks) Order() int { return 10 }

func (h *monitorWebhooks) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	// organize events by nodes
	eventsByNode := make(map[flows.NodeUUID][]*events.WebhookCalled)
	for _, es := range scenes {
		for _, e := range es {
			wc := e.(*WebhookCall)
			eventsByNode[wc.NodeUUID] = append(eventsByNode[wc.NodeUUID], wc.Event)
		}
	}

	unhealthyNodeUUIDs := make([]flows.NodeUUID, 0, 10)

	// record events against each node and determine if it's healthy
	for nodeUUID, events := range eventsByNode {
		node := &models.WebhookNode{UUID: nodeUUID}
		if err := node.Record(ctx, rt, events); err != nil {
			return fmt.Errorf("error recording events for webhook node: %w", err)
		}

		healthy, err := node.Healthy(ctx, rt)
		if err != nil {
			return fmt.Errorf("error getting health of webhook node: %w", err)
		}

		if !healthy {
			unhealthyNodeUUIDs = append(unhealthyNodeUUIDs, nodeUUID)
		}
	}

	// if we have unhealthy nodes, ensure we have an incident
	if len(unhealthyNodeUUIDs) > 0 {
		_, err := models.IncidentWebhooksUnhealthy(ctx, tx, rt.VK, oa, unhealthyNodeUUIDs)
		if err != nil {
			return fmt.Errorf("error creating unhealthy webhooks incident: %w", err)
		}
	}

	return nil
}
