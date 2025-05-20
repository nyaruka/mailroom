package models

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/gocommon/dates"
)

type DailyCount struct {
	OrgID OrgID      `db:"org_id"`
	Day   dates.Date `db:"day"`
	Scope string     `db:"scope"`
	Count int64      `db:"count"`
}

const sqlInsertDailyCount = `INSERT INTO orgs_dailycount(org_id, scope, day, count, is_squashed) VALUES(:org_id, :scope, :day, :count, FALSE)`

// InsertDailyCounts inserts daily counts for the given org for today.
func InsertDailyCounts(ctx context.Context, tx DBorTx, oa *OrgAssets, when time.Time, scopeCounts map[string]int) error {
	day := dates.ExtractDate(when.In(oa.Env().Timezone()))
	counts := make([]*DailyCount, 0, len(scopeCounts))

	for scope, count := range scopeCounts {
		counts = append(counts, &DailyCount{OrgID: oa.OrgID(), Day: day, Scope: scope, Count: int64(count)})
	}

	return BulkQuery(ctx, "inserted daily counts", tx, sqlInsertDailyCount, counts)
}

type FlowActivityCount struct {
	FlowID FlowID `db:"flow_id"`
	Scope  string `db:"scope"`
	Count  int    `db:"count"`
}

const sqlInsertFlowActivityCount = `INSERT INTO flows_flowactivitycount(flow_id, scope, count, is_squashed) VALUES(:flow_id, :scope, :count, FALSE)`

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
