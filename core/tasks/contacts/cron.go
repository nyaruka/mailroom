package contacts

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/campaigns"
	"github.com/nyaruka/mailroom/runtime"
)

func init() {
	tasks.RegisterCron("contact_fires", &FiresCron{fetchBatchSize: 5_000, taskBatchSize: 100})
}

type FiresCron struct {
	fetchBatchSize int
	taskBatchSize  int
}

func NewFiresCron(fetchBatchSize, taskBatchSize int) *FiresCron {
	return &FiresCron{fetchBatchSize: fetchBatchSize, taskBatchSize: taskBatchSize}
}

func (c *FiresCron) Next(last time.Time) time.Time {
	return tasks.CronNext(last, 30*time.Second)
}

func (c *FiresCron) AllInstances() bool {
	return false
}

func (c *FiresCron) Run(ctx context.Context, rt *runtime.Runtime) (map[string]any, error) {
	start := time.Now()
	numWaitTimeouts, numWaitExpires, numSessionExpires, numCampaignEvents := 0, 0, 0, 0

	rc := rt.RP.Get()
	defer rc.Close()

	for {
		fires, err := models.LoadDueContactfires(ctx, rt, c.fetchBatchSize)
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
			if f.Type == models.ContactFireTypeWaitTimeout {
				og.grouping = "wait_timeouts"
			} else if f.Type == models.ContactFireTypeWaitExpiration {
				og.grouping = "wait_expires"
			} else if f.Type == models.ContactFireTypeSessionExpiration {
				og.grouping = "session_expires"
			} else if f.Type == models.ContactFireTypeCampaignEvent {
				og.grouping = "campaign:" + f.Scope
			} else {
				return nil, fmt.Errorf("unknown contact fire type: %s", f.Type)
			}
			grouped[og] = append(grouped[og], f)
		}

		for og, fs := range grouped {
			for batch := range slices.Chunk(fs, c.taskBatchSize) {
				if og.grouping == "wait_timeouts" {
					// turn wait timeouts into bulk wait timeout tasks
					ts := make([]*WaitTimeout, len(batch))
					for i, f := range batch {
						ts[i] = &WaitTimeout{ContactID: f.ContactID, SessionUUID: flows.SessionUUID(f.SessionUUID), SprintUUID: flows.SprintUUID(f.SprintUUID)}
					}

					// queue to throttled queue but high priority so they get priority over flow starts etc
					if err := tasks.Queue(rc, tasks.ThrottledQueue, og.orgID, &BulkWaitTimeoutTask{Timeouts: ts}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk wait timeout task for org #%d: %w", og.orgID, err)
					}
					numWaitTimeouts += len(batch)
				} else if og.grouping == "wait_expires" {
					// turn wait expires into bulk wait expire tasks
					es := make([]*WaitExpiration, len(batch))
					for i, f := range batch {
						es[i] = &WaitExpiration{ContactID: f.ContactID, SessionUUID: flows.SessionUUID(f.SessionUUID), SprintUUID: flows.SprintUUID(f.SprintUUID)}
					}

					// put expirations in throttled queue but high priority so they get priority over flow starts etc
					if err := tasks.Queue(rc, tasks.ThrottledQueue, og.orgID, &BulkWaitExpireTask{Expirations: es}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk wait expire task for org #%d: %w", og.orgID, err)
					}
					numWaitExpires += len(batch)
				} else if og.grouping == "session_expires" {
					// turn session timeouts into bulk session expire tasks
					ss := make([]flows.SessionUUID, len(batch))
					for i, f := range batch {
						ss[i] = flows.SessionUUID(f.SessionUUID)
					}

					// queue to throttled queue but high priority so they get priority over flow starts etc
					if err := tasks.Queue(rc, tasks.ThrottledQueue, og.orgID, &BulkSessionExpireTask{SessionUUIDs: ss}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk session expire task for org #%d: %w", og.orgID, err)
					}
					numSessionExpires += len(batch)
				} else if strings.HasPrefix(og.grouping, "campaign:") {
					// turn campaign fires into bulk campaign tasks
					cids := make([]models.ContactID, len(batch))
					for i, f := range batch {
						cids[i] = f.ContactID
					}

					eventID, _ := strconv.Atoi(strings.TrimPrefix(og.grouping, "campaign:"))

					// queue to throttled queue with low priority
					if err := tasks.Queue(rc, tasks.ThrottledQueue, og.orgID, &campaigns.BulkCampaignTriggerTask{ContactIDs: cids, EventID: models.CampaignEventID(eventID)}, true); err != nil {
						return nil, fmt.Errorf("error queuing bulk campaign trigger task for org #%d: %w", og.orgID, err)
					}
					numCampaignEvents += len(batch)
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

	return map[string]any{"wait_timeouts": numWaitTimeouts, "wait_expires": numWaitExpires, "session_expires": numSessionExpires, "campaign_events": numCampaignEvents}, nil
}
