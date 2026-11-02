package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestLLMCalled(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	vc := rt.VK.Get()
	defer vc.Close()

	runTests(t, rt, "testdata/llm_called.json")

	assertvk.HGetAll(t, vc, "tallies:1", map[string]string{
		"llms/10002/calls/2025-05-04":      "2",
		"llms/10002/tokens:in/2025-05-04":  "79",
		"llms/10002/tokens:out/2025-05-04": "83",
	})
	assertvk.SMembers(t, vc, "tallies:pending", []string{"1"})
}
