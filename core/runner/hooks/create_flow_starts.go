package hooks

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/core/tasks"
	"github.com/nyaruka/mailroom/core/tasks/starts"
	"github.com/nyaruka/mailroom/runtime"
)

// CreateFlowStarts is our hook to fire our scene starts
var CreateFlowStarts runner.PreCommitHook = &createFlowStarts{}

type createFlowStarts struct{}

func (h *createFlowStarts) Order() int { return 1 }

func (h *createFlowStarts) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	rc := rt.RP.Get()
	defer rc.Close()

	// for each of our scene
	for _, es := range scenes {
		for _, e := range es {
			event := e.(*events.SessionTriggeredEvent)

			// look up our flow
			f, err := oa.FlowByUUID(event.Flow.UUID)
			if err != nil {
				return fmt.Errorf("unable to load flow with UUID: %s: %w", event.Flow.UUID, err)
			}
			flow := f.(*models.Flow)

			// load our groups by uuid
			groupIDs := make([]models.GroupID, 0, len(event.Groups))
			for i := range event.Groups {
				group := oa.GroupByUUID(event.Groups[i].UUID)
				if group != nil {
					groupIDs = append(groupIDs, group.ID())
				}
			}

			// load our contacts by uuid
			contactIDs, err := models.GetContactIDsFromReferences(ctx, tx, oa.OrgID(), event.Contacts)
			if err != nil {
				return fmt.Errorf("error loading contacts by reference: %w", err)
			}

			historyJSON, err := jsonx.Marshal(event.History)
			if err != nil {
				return fmt.Errorf("error marshaling session history: %w", err)
			}

			// create our start
			start := models.NewFlowStart(oa.OrgID(), models.StartTypeFlowAction, flow.ID()).
				WithGroupIDs(groupIDs).
				WithContactIDs(contactIDs).
				WithURNs(event.URNs).
				WithQuery(event.ContactQuery).
				WithExcludeInAFlow(event.Exclusions.InAFlow).
				WithCreateContact(event.CreateContact).
				WithParentSummary(event.RunSummary).
				WithSessionHistory(historyJSON)

			// TODO find another way to pass start info to new calls
			if flow.FlowType() == models.FlowTypeVoice {
				if err := models.InsertFlowStarts(ctx, tx, []*models.FlowStart{start}); err != nil {
					return fmt.Errorf("error inserting flow start: %w", err)
				}
			}

			err = tasks.Queue(rc, tasks.BatchQueue, oa.OrgID(), &starts.StartFlowTask{FlowStart: start}, false)
			if err != nil {
				return fmt.Errorf("error queuing flow start: %w", err)
			}
		}
	}

	return nil
}
