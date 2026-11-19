package models

import (
	"context"

	"github.com/vinovest/sqlx"
)

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
