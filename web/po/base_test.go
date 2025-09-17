package po_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestImportAndExport(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/export.json")
	testsuite.RunWebTests(t, ctx, rt, "testdata/import.json")
}
