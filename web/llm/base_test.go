package llm_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestTranslate(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, ctx, rt, "testdata/translate.json", testsuite.ResetNone)
}
