package crons

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/core/tasks"
	"github.com/nyaruka/mailroom/v26/runtime"
)

func init() {
	Register("contact_fires", &FireContactsCron{FetchBatchSize: 5_000, TaskBatchSize: 100, FlowBatchSize: tasks.FlowStartBatchSize})
}

type FireContactsCron struct {
	FetchBatchSize int
	TaskBatchSize  int
	FlowBatchSize  int
}

func (c *FireContactsCron) Next(last time.Time) time.Time {
	return Next(last, 30*time.Second)
}

func (c *FireContactsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	start := time.Now()
	counts := map[string]int{"wait_timeouts": 0, "wait_expires": 0, "session_expires": 0, "campaign_points": 0, "ticket_closes": 0}

	for {
		fires, err := models.LoadDueContactfires(ctx, rt, c.FetchBatchSize)
		if err != nil {
			return nil, fmt.Errorf("error loading due contact fires: %w", err)
		}
		if len(fires) == 0 {
			break
		}

		// organize fires by the bulk tasks they'll be batched into
		grouped := make(map[fireGroup][]*models.ContactFire, 25)

		// unique campaign point IDs
		pointIDs := make(map[models.PointID]bool)

		for _, f := range fires {
			g := fireGroup{orgID: f.OrgID}
			switch f.Type {
			case models.ContactFireTypeWaitTimeout:
				g.grouping = "wait_timeouts"
			case models.ContactFireTypeWaitExpiration:
				g.grouping = "wait_expires"
			case models.ContactFireTypeSessionExpiration:
				g.grouping = "session_expires"
			case models.ContactFireTypeCampaignPoint:
				g.grouping = "campaign:" + f.Scope

				pointID, _ := parseCampaignFireScope(f.Scope)
				pointIDs[pointID] = true
			case models.ContactFireTypeTicketClose:
				g.grouping = "ticket_closes"
			default:
				return nil, fmt.Errorf("unknown contact fire type: %s", f.Type)
			}
			grouped[g] = append(grouped[g], f)
		}

		// look up point types for all campaign fires in this batch
		pointTypes, err := models.GetCampaignPointTypes(ctx, rt.DB, slices.Collect(maps.Keys(pointIDs)))
		if err != nil {
			return nil, fmt.Errorf("error looking up campaign point types: %w", err)
		}

		for g, fs := range grouped {
			// each batch becomes a single task, and flow campaign points use smaller batches
			batchSize := c.TaskBatchSize
			if pointID, _, ok := g.campaignPoint(); ok && pointTypes[pointID] == models.PointTypeFlow {
				batchSize = min(batchSize, c.FlowBatchSize)
			}

			for batch := range slices.Chunk(fs, batchSize) {
				numFired, err := c.fireBatch(ctx, rt, g, batch)
				if err != nil {
					return nil, err
				}
				counts[g.countKey()] += numFired
			}
		}

		// if we're getting close to the repeat schedule of this task, stop and let the next run pick up the rest
		if time.Since(start) > 25*time.Second {
			break
		}
	}

	res := make(map[string]any, len(counts))
	for k, v := range counts {
		res[k] = v
	}
	return res, nil
}

type fireGroup struct {
	orgID    models.OrgID
	grouping string
}

func (g fireGroup) countKey() string {
	if strings.HasPrefix(g.grouping, "campaign:") {
		return "campaign_points"
	}
	return g.grouping
}

func (g fireGroup) campaignPoint() (models.PointID, int, bool) {
	scope, ok := strings.CutPrefix(g.grouping, "campaign:")
	if !ok {
		return 0, 0, false
	}
	pointID, fireVersion := parseCampaignFireScope(scope)
	return pointID, fireVersion, true
}

// fireBatch claims the given fires by deleting them in a transaction, queues a task for the ones claimed, and only then
// commits. Campaign tasks aren't idempotent, so a fire must never be queued again after its task was queued: if the
// delete fails nothing is queued, and if queuing fails the rollback leaves the fires to be retried.
func (c *FireContactsCron) fireBatch(ctx context.Context, rt *runtime.Runtime, g fireGroup, batch []*models.ContactFire) (int, error) {
	tx, err := rt.DB.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("error beginning transaction to claim contact fires: %w", err)
	}
	defer tx.Rollback()

	claimed, err := models.DeleteContactFires(ctx, tx, batch)
	if err != nil {
		return 0, fmt.Errorf("error claiming contact fires: %w", err)
	}

	if len(claimed) > 0 {
		if err := c.queueTask(ctx, rt, g, claimed); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("error committing claimed contact fires: %w", err)
	}

	return len(claimed), nil
}

func (c *FireContactsCron) queueTask(ctx context.Context, rt *runtime.Runtime, g fireGroup, batch []*models.ContactFire) error {
	if g.grouping == "wait_timeouts" {
		// turn wait timeouts into bulk wait timeout tasks
		ts := make([]*tasks.WaitTimeout, len(batch))
		for i, f := range batch {
			ts[i] = &tasks.WaitTimeout{ContactID: f.ContactID, SessionUUID: core.SessionUUID(f.SessionUUID), SprintUUID: flows.SprintUUID(f.SprintUUID)}
		}

		// queue to throttled queue but high priority
		if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, g.orgID, &tasks.BulkWaitTimeout{Timeouts: ts}, true); err != nil {
			return fmt.Errorf("error queuing bulk wait timeout task for org #%d: %w", g.orgID, err)
		}
	} else if g.grouping == "wait_expires" {
		// turn wait expires into bulk wait expire tasks
		es := make([]*tasks.WaitExpiration, len(batch))
		for i, f := range batch {
			es[i] = &tasks.WaitExpiration{ContactID: f.ContactID, SessionUUID: core.SessionUUID(f.SessionUUID), SprintUUID: flows.SprintUUID(f.SprintUUID)}
		}

		// queue to throttled queue but high priority
		if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, g.orgID, &tasks.BulkWaitExpire{Expirations: es}, true); err != nil {
			return fmt.Errorf("error queuing bulk wait expire task for org #%d: %w", g.orgID, err)
		}
	} else if g.grouping == "session_expires" {
		// turn session timeouts into bulk session expire tasks
		ss := make([]models.SessionRef, len(batch))
		for i, f := range batch {
			ss[i] = models.SessionRef{UUID: core.SessionUUID(f.SessionUUID), ContactID: f.ContactID}
		}

		// queue to batch queue rather than throttled, since expiring sessions can't create more messages
		if err := tasks.Queue(ctx, rt, rt.Queues.Batch, g.orgID, &tasks.InterruptSessionBatch{Sessions: ss, Status: flows.SessionStatusExpired}, false); err != nil {
			return fmt.Errorf("error queuing bulk session expire task for org #%d: %w", g.orgID, err)
		}
	} else if g.grouping == "ticket_closes" {
		// turn ticket closes into bulk ticket inactive tasks
		ts := make([]*tasks.InactiveTicketRef, len(batch))
		for i, f := range batch {
			ts[i] = &tasks.InactiveTicketRef{ContactID: f.ContactID, TicketUUID: core.TicketUUID(f.Scope)}
		}

		if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, g.orgID, &tasks.BulkTicketInactive{Tickets: ts}, false); err != nil {
			return fmt.Errorf("error queuing bulk ticket inactive task for org #%d: %w", g.orgID, err)
		}
	} else if pointID, fireVersion, ok := g.campaignPoint(); ok {
		// turn campaign fires into bulk campaign tasks
		cids := make([]models.ContactID, len(batch))
		for i, f := range batch {
			cids[i] = f.ContactID
		}

		// queue to throttled queue but high priority
		if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, g.orgID, &tasks.BulkCampaignTrigger{PointID: pointID, FireVersion: fireVersion, ContactIDs: cids}, true); err != nil {
			return fmt.Errorf("error queuing bulk campaign trigger task for org #%d: %w", g.orgID, err)
		}
	}

	return nil
}

var campaignEventScopePattern = regexp.MustCompile(`^(\d+):(\d+)$`)

func parseCampaignFireScope(scope string) (models.PointID, int) {
	var pointID, fireVersion int
	match := campaignEventScopePattern.FindStringSubmatch(scope)
	if len(match) > 1 {
		pointID, _ = strconv.Atoi(match[1])
	}
	if len(match) > 2 {
		fireVersion, _ = strconv.Atoi(match[2])
	}

	return models.PointID(pointID), fireVersion
}
