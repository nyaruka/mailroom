package campaign_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestSchedule(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(t, testsuite.ResetData)

	testsuite.RunWebTests(t, ctx, rt, "testdata/schedule.json", testsuite.ResetValkey)
}
