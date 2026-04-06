package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestContactGroupsChanged(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	runTests(t, rt, "testdata/contact_groups_changed.json")
}
