package system_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestQueues(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetNone)

	testsuite.RunWebTests(t, ctx, rt, "testdata/queues.json", testsuite.ResetNone)
}
