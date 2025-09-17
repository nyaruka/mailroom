package po_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestImportAndExport(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/export.json")
	testsuite.RunWebTests(t, rt, "testdata/import.json")
}
