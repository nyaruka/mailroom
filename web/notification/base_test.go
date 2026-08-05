package notification_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestPublish(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/publish.json")
}
