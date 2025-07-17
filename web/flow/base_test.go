package flow_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestChangeLanguage(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/change_language.json", nil)
}

func TestClone(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/clone.json", nil)
}

func TestInspect(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/inspect.json", nil)
}

func TestMigrate(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/migrate.json", nil)
}

func TestStart(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	// TODO TestTwilioIVR blows up without full reset so some prior test isn't cleaning up after itself
	//defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)
	defer testsuite.Reset(testsuite.ResetAll)

	testsuite.RunWebTests(t, ctx, rt, "testdata/start.json", nil)

	testsuite.AssertBatchTasks(t, testdb.Org1.ID, map[string]int{"start_flow": 1})
}

func TestStartPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/start_preview.json", nil)
}
