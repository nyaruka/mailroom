package campaign_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestSchedule(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/schedule.json")
}
