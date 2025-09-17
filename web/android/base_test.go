package android_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestEvent(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/event.json")
}

func TestMessage(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/message.json")
}

func TestSync(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	testdb.InsertChannel(rt, testdb.Org1, "A", "Android 1", "123", []string{"tel"}, "SR", map[string]any{})

	testsuite.RunWebTests(t, ctx, rt, "testdata/sync.json")
}
