package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestContactStatusChanged(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetDB)

	runTests(t, rt, "testdata/contact_status_changed.json")
}
