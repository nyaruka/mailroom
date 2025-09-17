package system_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestErrors(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/errors.json")
}

func TestQueues(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/queues.json")
}
