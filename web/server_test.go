package web_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestServer(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/server.json")
}
