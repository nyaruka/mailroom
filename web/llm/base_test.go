package llm_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
)

func TestTranslate(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/translate.json")
}
