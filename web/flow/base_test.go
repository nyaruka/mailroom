package flow_test

import (
	"fmt"
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
)

func TestChangeLanguage(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/change_language.json")
}

func TestClone(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/clone.json")
}

func TestInspect(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/inspect.json")
}

func TestInterrupt(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey)

	// set the progress key for PickANumber to simulate an ongoing interruption for that flow
	vc := rt.VK.Get()
	vc.Do("SET", fmt.Sprintf("%s:%d", "interrupt_flow_progress", testdb.PickANumber.ID), 100, "EX", 15*60)
	vc.Close()

	testsuite.RunWebTests(t, rt, "testdata/interrupt.json")
}

func TestMigrate(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/migrate.json")
}

func TestStart(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// TODO TestTwilioIVR blows up without full reset so some prior test isn't cleaning up after itself
	//defer testsuite.Reset(t, rt, testsuite.ResetData|testsuite.ResetValkey)
	defer testsuite.Reset(t, rt, testsuite.ResetAll)

	testsuite.RunWebTests(t, rt, "testdata/start.json")
}

func TestStartPreview(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/start_preview.json")
}
