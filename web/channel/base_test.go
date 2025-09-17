package channel_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestInterrupt(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	testsuite.RunWebTests(t, rt, "testdata/interrupt.json")
}
