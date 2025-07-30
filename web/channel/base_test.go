package channel_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestInterrupt(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(t, testsuite.ResetAll)

	testsuite.RunWebTests(t, ctx, rt, "testdata/interrupt.json", testsuite.ResetValkey)
}
