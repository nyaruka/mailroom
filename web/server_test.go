package web_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestServer(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/server.json", testsuite.ResetNone)
}
