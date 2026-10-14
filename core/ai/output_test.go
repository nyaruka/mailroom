package ai_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/stretchr/testify/assert"
)

func TestStripFence(t *testing.T) {
	tcs := []struct {
		output   string
		stripped string
	}{
		{output: " Hello\n", stripped: "Hello"},
		{output: "```json\n{\"a\": 1}\n```", stripped: `{"a": 1}`},
		{output: "```\n<CANT>\n```", stripped: "<CANT>"},
		{output: "```Hotels```", stripped: "Hotels"},
		{output: "``` \n Hello \n```", stripped: "Hello"},
		{output: "Hello ```code```", stripped: "Hello ```code```"},
	}

	for _, tc := range tcs {
		assert.Equal(t, tc.stripped, ai.StripFence(tc.output), "stripped mismatch for %q", tc.output)
	}
}
