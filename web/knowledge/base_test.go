package knowledge_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
)

func TestIndex(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	rt.Config.EmbeddingsEndpoint = "http://embeddings:8095/v1"

	testsuite.RunWebTests(t, rt, "testdata/index.json")
}

func TestIndexDisabled(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/index_disabled.json")
}

func TestSearch(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	testsuite.RunWebTests(t, rt, "testdata/search.json")
}
