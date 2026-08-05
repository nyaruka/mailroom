package knowledge_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestSearch(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/search.json")
}
