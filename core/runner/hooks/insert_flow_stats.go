package hooks

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/buger/jsonparser"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/core/runner"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/vkutil"
)

const (
	recentContactsCap    = 5              // number of recent contacts we keep per segment
	recentContactsExpire = time.Hour * 24 // how long we keep recent contacts
	recentContactsKey    = "recent_contacts:%s:%s"
)

var storeOperandsForTypes = map[string]bool{"wait_for_response": true, "split_by_expression": true, "split_by_contact_field": true, "split_by_run_result": true}

// InsertFlowStats is our hook for inserting flow stats
var InsertFlowStats runner.PreCommitHook = &insertFlowStats{}

type insertFlowStats struct{}

func (h *insertFlowStats) Order() int { return 1 }

func (h *insertFlowStats) Execute(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, oa *models.OrgAssets, scenes map[*runner.Scene][]any) error {
	countsBySegment := make(map[segmentInfo]int, 10)
	recentBySegment := make(map[segmentInfo][]*segmentRecentContact, 10)
	categoryChanges := make(map[resultInfo]int, 10)
	nodeTypeCache := make(map[flows.NodeUUID]string)

	// scenes are processed in order of session UUID.. solely for the sake of test determinism
	for _, scene := range slices.SortedStableFunc(maps.Keys(scenes), func(s1, s2 *runner.Scene) int { return cmp.Compare(s1.SessionUUID(), s2.SessionUUID()) }) {
		for _, seg := range scene.Sprint.Segments() {
			segID := segmentInfo{
				flowID:   seg.Flow().Asset().(*models.Flow).ID(),
				exitUUID: seg.Exit().UUID(),
				destUUID: seg.Destination().UUID(),
			}

			countsBySegment[segID]++

			// only store recent contact if we have less than the cap
			if len(recentBySegment[segID]) < recentContactsCap {
				uiNodeType := getNodeUIType(seg.Flow(), seg.Node(), nodeTypeCache)

				// only store operand values for certain node types
				operand := ""
				if storeOperandsForTypes[uiNodeType] {
					operand = seg.Operand()
				}
				recentBySegment[segID] = append(recentBySegment[segID], &segmentRecentContact{contact: scene.Contact, operand: operand, time: seg.Time(), rnd: vkutil.RandomBase64(10)})
			}
		}

		for _, e := range scene.Sprint.Events() {
			switch typed := e.(type) {
			case *events.RunResultChangedEvent:
				flow, _ := scene.LocateEvent(e)
				resultKey := utils.Snakify(typed.Name)
				if typed.Previous != nil {
					categoryChanges[resultInfo{flowID: flow.ID(), result: resultKey, category: typed.Previous.Category}]--
				}
				categoryChanges[resultInfo{flowID: flow.ID(), result: resultKey, category: typed.Category}]++
			}
		}
	}

	activityCounts := make([]*models.FlowActivityCount, 0, len(countsBySegment))
	for seg, count := range countsBySegment {
		if count != 0 {
			activityCounts = append(activityCounts, &models.FlowActivityCount{
				FlowID: seg.flowID,
				Scope:  fmt.Sprintf("segment:%s:%s", seg.exitUUID, seg.destUUID),
				Count:  count,
			})
		}
	}

	if err := models.InsertFlowActivityCounts(ctx, tx, activityCounts); err != nil {
		return fmt.Errorf("error inserting flow activity counts: %w", err)
	}

	resultCounts := make([]*models.FlowResultCount, 0, len(categoryChanges))
	for res, count := range categoryChanges {
		if count != 0 {
			resultCounts = append(resultCounts, &models.FlowResultCount{
				FlowID:   res.flowID,
				Result:   res.result,
				Category: res.category,
				Count:    count,
			})
		}
	}

	if err := models.InsertFlowResultCounts(ctx, tx, resultCounts); err != nil {
		return fmt.Errorf("error inserting flow result counts: %w", err)
	}

	rc := rt.RP.Get()
	defer rc.Close()

	for segID, recentContacts := range recentBySegment {
		recentSet := vkutil.NewCappedZSet(fmt.Sprintf(recentContactsKey, segID.exitUUID, segID.destUUID), recentContactsCap, recentContactsExpire)

		for _, recent := range recentContacts {
			// set members need to be unique, so we include a random string
			value := fmt.Sprintf("%s|%d|%s", recent.rnd, recent.contact.ID(), stringsx.TruncateEllipsis(recent.operand, 100))
			score := float64(recent.time.UnixNano()) / float64(1e9) // score is UNIX time as floating point

			err := recentSet.Add(ctx, rc, value, score)
			if err != nil {
				return fmt.Errorf("error adding recent contact to set: %w", err)
			}
		}
	}

	return nil
}

type resultInfo struct {
	flowID   models.FlowID
	result   string
	category string
}

type segmentInfo struct {
	flowID   models.FlowID
	exitUUID flows.ExitUUID
	destUUID flows.NodeUUID
}

type segmentRecentContact struct {
	contact *flows.Contact
	operand string
	time    time.Time
	rnd     string
}

func getNodeUIType(flow flows.Flow, node flows.Node, cache map[flows.NodeUUID]string) string {
	uiType, cached := cache[node.UUID()]
	if cached {
		return uiType
	}

	// try to lookup node type but don't error if we can't find it.. could be a bad flow
	value, _ := jsonparser.GetString(flow.UI(), "nodes", string(node.UUID()), "type")
	cache[node.UUID()] = value
	return value
}
