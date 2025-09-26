package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/testsuite"
)

func TestSessionTriggered(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	reset := test.MockUniverse()
	defer reset()

	runTests(t, rt, "testdata/session_triggered.json", true)
}

func TestSessionTriggeredByQuery(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	runTests(t, rt, "testdata/session_triggered_by_query.json", true)
}
