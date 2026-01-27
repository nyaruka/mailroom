package crons

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	Register("contact_fires", &FireContactsCron{FetchBatchSize: 5_000, TaskBatchSize: 100})
}

type FireContactsCron struct {
	FetchBatchSize int
	TaskBatchSize  int
}

func (c *FireContactsCron) Next(last time.Time) time.Time {
	return Next(last, 30*time.Second)
}

func (c *FireContactsCron) AllInstances() bool {
	return false
}

func (c *FireContactsCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	start := time.Now()
	numWaitTimeouts, numWaitExpires, numSessionExpires, numCampaignPoints := 0, 0, 0, 0

	for {
		fires, err := models.LoadDueContactfires(ctx, rt, c.FetchBatchSize)
		if err != nil {
			return nil, fmt.Errorf("error loading due contact fires: %w", err)
		}
		if len(fires) == 0 {
			break
		}

		// organize fires by the bulk tasks they'll be batched into
		type orgAndGrouping struct {
			orgID    models.OrgID
			grouping string
		}
		grouped := make(map[orgAndGrouping][]*models.ContactFire, 25)
		for _, f := range fires {
			og := orgAndGrouping{orgID: f.OrgID}
			switch f.Type {
			case models.ContactFireTypeWaitTimeout:
				og.grouping = "wait_timeouts"
			case models.ContactFireTypeWaitExpiration:
				og.grouping = "wait_expires"
			case models.ContactFireTypeSessionExpiration:
				og.grouping = "session_expires"
			case models.ContactFireTypeCampaignPoint:
				og.grouping = "campaign:" + f.Scope
			default:
				return nil, fmt.Errorf("unknown contact fire type: %s", f.Type)
			}
			grouped[og] = append(grouped[og], f)
		}

		for og, fs := range grouped {
			for batch := range slices.Chunk(fs, c.TaskBatchSize) {
				if og.grouping == "wait_timeouts" {
					// turn wait timeouts into bulk wait timeout tasks
					ts := make([]*tasks.WaitTimeout, len(batch))
					for i, f := range batch {
						ts[i] = &tasks.WaitTimeout{ContactID: f.ContactID, SessionUUID: flows.SessionUUID(f.SessionUUID), SprintUUID: flows.SprintUUID(f.SprintUUID)}
					}

					// queue to throttled queue but high priority
					if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, og.orgID, &tasks.BulkWaitTimeout{Timeouts: ts}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk wait timeout task for org #%d: %w", og.orgID, err)
					}
					numWaitTimeouts += len(batch)
				} else if og.grouping == "wait_expires" {
					// turn wait expires into bulk wait expire tasks
					es := make([]*tasks.WaitExpiration, len(batch))
					for i, f := range batch {
						es[i] = &tasks.WaitExpiration{ContactID: f.ContactID, SessionUUID: flows.SessionUUID(f.SessionUUID), SprintUUID: flows.SprintUUID(f.SprintUUID)}
					}

					// queue to throttled queue but high priority
					if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, og.orgID, &tasks.BulkWaitExpire{Expirations: es}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk wait expire task for org #%d: %w", og.orgID, err)
					}
					numWaitExpires += len(batch)
				} else if og.grouping == "session_expires" {
					// turn session timeouts into bulk session expire tasks
					ss := make([]models.SessionRef, len(batch))
					for i, f := range batch {
						ss[i] = models.SessionRef{UUID: flows.SessionUUID(f.SessionUUID), ContactID: f.ContactID}
					}

					// queue to batch queue rather than throttled, since expiring sessions can't create more messages
					if err := tasks.Queue(ctx, rt, rt.Queues.Batch, og.orgID, &tasks.InterruptSessionBatch{Sessions: ss, Status: flows.SessionStatusExpired}, false); err != nil {
						return nil, fmt.Errorf("error queuing bulk session expire task for org #%d: %w", og.orgID, err)
					}
					numSessionExpires += len(batch)
				} else if strings.HasPrefix(og.grouping, "campaign:") {
					// turn campaign fires into bulk campaign tasks
					cids := make([]models.ContactID, len(batch))
					for i, f := range batch {
						cids[i] = f.ContactID
					}

					pointID, fireVersion := c.parseCampaignFireScope(strings.TrimPrefix(og.grouping, "campaign:"))

					// queue to throttled queue but high priority
					if err := tasks.Queue(ctx, rt, rt.Queues.Throttled, og.orgID, &tasks.BulkCampaignTrigger{PointID: pointID, FireVersion: fireVersion, ContactIDs: cids}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk campaign trigger task for org #%d: %w", og.orgID, err)
					}
					numCampaignPoints += len(batch)
				}

				if err := models.DeleteContactFires(ctx, rt, batch); err != nil {
					return nil, fmt.Errorf("error deleting queued contact fires: %w", err)
				}
			}
		}

		// if we're getting close to the repeat schedule of this task, stop and let the next run pick up the rest
		if time.Since(start) > 25*time.Second {
			break
		}
	}

	return map[string]any{"wait_timeouts": numWaitTimeouts, "wait_expires": numWaitExpires, "session_expires": numSessionExpires, "campaign_points": numCampaignPoints}, nil
}

var campaignEventScopePattern = regexp.MustCompile(`^(\d+):(\d+)$`)

func (c *FireContactsCron) parseCampaignFireScope(scope string) (models.PointID, int) {
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
