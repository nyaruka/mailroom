package flow_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestChangeLanguage(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/change_language.json", nil, testsuite.ResetNone)
}

func TestClone(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/clone.json", nil, testsuite.ResetNone)
}

func TestInspect(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/inspect.json", nil, testsuite.ResetNone)
}

func TestMigrate(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/migrate.json", nil, testsuite.ResetNone)
}

func TestStart(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	// TODO TestTwilioIVR blows up without full reset so some prior test isn't cleaning up after itself
	//defer testsuite.Reset(testsuite.ResetData | testsuite.ResetValkey)
	defer testsuite.Reset(testsuite.ResetAll)

	testsuite.RunWebTests(t, ctx, rt, "testdata/start.json", nil, testsuite.ResetValkey)
}

func TestStartPreview(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/start_preview.json", nil, testsuite.ResetNone)
}
