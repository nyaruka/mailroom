package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestRunStarted(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	runTests(t, rt, "testdata/run_started.json")
}
