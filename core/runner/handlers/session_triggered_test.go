package handlers_test

import (
	"testing"

	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestSessionTriggered(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	reset := test.MockUniverse()
	defer reset()

	runTests(t, rt, "testdata/session_triggered.json")
}

func TestSessionTriggeredByQuery(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	runTests(t, rt, "testdata/session_triggered_by_query.json")
}
