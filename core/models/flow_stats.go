package models

import (
	"context"
	"fmt"
	"time"

	"github.com/buger/jsonparser"
	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/stringsx"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/goflow/utils"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/redisx"
)

const (
	recentContactsCap    = 5              // number of recent contacts we keep per segment
	recentContactsExpire = time.Hour * 24 // how long we keep recent contacts
	recentContactsKey    = "recent_contacts:%s:%s"
)

var storeOperandsForTypes = map[string]bool{"wait_for_response": true, "split_by_expression": true, "split_by_contact_field": true, "split_by_run_result": true}

type FlowActivityCount struct {
	FlowID FlowID `db:"flow_id"`
	Scope  string `db:"scope"`
	Count  int    `db:"count"`
}

const sqlInsertFlowActivityCount = `
INSERT INTO flows_flowactivitycount( flow_id,  scope,  count,  is_squashed)
                             VALUES(:flow_id, :scope, :count,        FALSE)
`

// InsertFlowActivityCounts inserts the given flow activity counts into the database
func InsertFlowActivityCounts(ctx context.Context, tx *sqlx.Tx, counts []*FlowActivityCount) error {
	return BulkQuery(ctx, "insert flow activity counts", tx, sqlInsertFlowActivityCount, counts)
}

type FlowResultCount struct {
	FlowID   FlowID `db:"flow_id"`
	Result   string `db:"result"`
	Category string `db:"category"`
	Count    int    `db:"count"`
}

const sqlInsertFlowResultCount = `
INSERT INTO flows_flowresultcount( flow_id,  result,  category,  count,  is_squashed)
                           VALUES(:flow_id, :result, :category, :count,        FALSE)
`

// InsertFlowResultCounts inserts the given flow result counts into the database
func InsertFlowResultCounts(ctx context.Context, tx *sqlx.Tx, counts []*FlowResultCount) error {
	return BulkQuery(ctx, "insert flow result counts", tx, sqlInsertFlowResultCount, counts)
}

type resultInfo struct {
	flowID   FlowID
	result   string
	category string
}

type segmentInfo struct {
	flowID   FlowID
	exitUUID flows.ExitUUID
	destUUID flows.NodeUUID
}

type segmentRecentContact struct {
	contact *flows.Contact
	operand string
	time    time.Time
	rnd     string
}

// RecordFlowStatistics records statistics from the given parallel slices of sessions and sprints
func RecordFlowStatistics(ctx context.Context, rt *runtime.Runtime, tx *sqlx.Tx, sessions []flows.Session, sprints []flows.Sprint) error {
	countsBySegment := make(map[segmentInfo]int, 10)
	recentBySegment := make(map[segmentInfo][]*segmentRecentContact, 10)
	categoryChanges := make(map[resultInfo]int, 10)
	nodeTypeCache := make(map[flows.NodeUUID]string)

	for i, sprint := range sprints {
		session := sessions[i]

		for _, seg := range sprint.Segments() {
			segID := segmentInfo{
				flowID:   seg.Flow().Asset().(*Flow).ID(),
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
				recentBySegment[segID] = append(recentBySegment[segID], &segmentRecentContact{contact: session.Contact(), operand: operand, time: seg.Time(), rnd: redisx.RandomBase64(10)})
			}
		}

		for _, e := range sprint.Events() {
			switch typed := e.(type) {
			case *events.RunResultChangedEvent:
				run, _ := session.FindStep(e.StepUUID())
				flow := run.Flow().Asset().(*Flow)
				resultKey := utils.Snakify(typed.Name)
				if typed.Previous != nil {
					categoryChanges[resultInfo{flowID: flow.ID(), result: resultKey, category: typed.Previous.Category}]--
				}
				categoryChanges[resultInfo{flowID: flow.ID(), result: resultKey, category: typed.Category}]++
			}
		}
	}

	activityCounts := make([]*FlowActivityCount, 0, len(countsBySegment))
	for seg, count := range countsBySegment {
		if count != 0 {
			activityCounts = append(activityCounts, &FlowActivityCount{
				FlowID: seg.flowID,
				Scope:  fmt.Sprintf("segment:%s:%s", seg.exitUUID, seg.destUUID),
				Count:  count,
			})
		}
	}

	if err := InsertFlowActivityCounts(ctx, tx, activityCounts); err != nil {
		return fmt.Errorf("error inserting flow activity counts: %w", err)
	}

	resultCounts := make([]*FlowResultCount, 0, len(categoryChanges))
	for res, count := range categoryChanges {
		if count != 0 {
			resultCounts = append(resultCounts, &FlowResultCount{
				FlowID:   res.flowID,
				Result:   res.result,
				Category: res.category,
				Count:    count,
			})
		}
	}

	if err := InsertFlowResultCounts(ctx, tx, resultCounts); err != nil {
		return fmt.Errorf("error inserting flow result counts: %w", err)
	}

	rc := rt.RP.Get()
	defer rc.Close()

	for segID, recentContacts := range recentBySegment {
		recentSet := redisx.NewCappedZSet(fmt.Sprintf(recentContactsKey, segID.exitUUID, segID.destUUID), recentContactsCap, recentContactsExpire)

		for _, recent := range recentContacts {
			// set members need to be unique, so we include a random string
			value := fmt.Sprintf("%s|%d|%s", recent.rnd, recent.contact.ID(), stringsx.TruncateEllipsis(recent.operand, 100))
			score := float64(recent.time.UnixNano()) / float64(1e9) // score is UNIX time as floating point

			err := recentSet.Add(rc, value, score)
			if err != nil {
				return fmt.Errorf("error adding recent contact to set: %w", err)
			}
		}
	}

	return nil
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
