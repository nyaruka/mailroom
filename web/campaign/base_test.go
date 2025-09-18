package campaign_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestSchedule(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, rt, "testdata/schedule.json")
}
