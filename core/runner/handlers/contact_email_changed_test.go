package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestContactEmailChanged(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	runTests(t, rt, "testdata/contact_email_changed.json")
}
