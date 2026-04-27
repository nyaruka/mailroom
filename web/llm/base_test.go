package llm_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
)

func TestTranslate(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	// LLM without the translation role - id will be 30000
	testdb.InsertLLM(t, rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "test", "gpt-4", "Flows Only", map[string]any{}, "F")

	testsuite.RunWebTests(t, rt, "testdata/translate.json")
}
