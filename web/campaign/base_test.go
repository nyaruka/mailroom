package campaign_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestSchedule(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData)

	testsuite.RunWebTests(t, ctx, rt, "testdata/schedule.json", map[string]string{
		"point1_id": fmt.Sprint(testdb.RemindersPoint1.ID),
	}, testsuite.ResetValkey)
}
