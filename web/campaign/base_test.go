package campaign_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestSchedule_event(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/schedule_event.json", map[string]string{
		"event1_id": fmt.Sprint(testdb.RemindersPoint1.ID),
	})

	testsuite.AssertBatchTasks(t, testdb.Org1.ID, map[string]int{"schedule_campaign_event": 1})
}
