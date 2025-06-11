package llm_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestTranslate(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	testsuite.RunWebTests(t, ctx, rt, "testdata/translate.json", nil)
}
