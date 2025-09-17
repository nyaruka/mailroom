package flow_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestChangeLanguage(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/change_language.json")
}

func TestClone(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/clone.json")
}

func TestInspect(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/inspect.json")
}

func TestInterrupt(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	testsuite.RunWebTests(t, ctx, rt, "testdata/interrupt.json")
}

func TestMigrate(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/migrate.json")
}

func TestStart(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	// TODO TestTwilioIVR blows up without full reset so some prior test isn't cleaning up after itself
	//defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)
	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	testsuite.RunWebTests(t, ctx, rt, "testdata/start.json")
}

func TestStartPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/start_preview.json")
}
