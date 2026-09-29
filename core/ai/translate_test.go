package ai_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/stretchr/testify/assert"
)

func TestTranslateByPrompt(t *testing.T) {
	ctx := context.Background()
	items := map[string][]string{"a:text": {"Hello"}, "a:quick_replies": {"Yes", "No"}, "b:arguments": {"yes"}}

	tcs := []struct {
		output   string
		expected map[string][]string
	}{
		{
			output:   `{"a:text": ["Hola"], "a:quick_replies": ["Sí", "No"], "b:arguments": ["sí"]}`,
			expected: map[string][]string{"a:text": {"Hola"}, "a:quick_replies": {"Sí", "No"}, "b:arguments": {"sí"}},
		},
		{ // items which are missing, the wrong length, contain <CANT> or weren't asked for are dropped
			output:   `{"a:text": ["Hola"], "a:quick_replies": ["Sí"], "b:arguments": ["<CANT>"], "c:text": ["Adiós"]}`,
			expected: map[string][]string{"a:text": {"Hola"}},
		},
		{ // wrapped in a code fence
			output:   "```json\n{\"a:text\": [\"Hola\"]}\n```",
			expected: map[string][]string{"a:text": {"Hola"}},
		},
		{output: `<CANT>`, expected: map[string][]string{}},
		{output: `not JSON`, expected: map[string][]string{}},
	}

	for _, tc := range tcs {
		svc := &promptService{output: tc.output}
		trans, err := ai.TranslateByPrompt(ctx, svc, "eng", "spa", items, 1234)
		assert.NoError(t, err)
		assert.Equal(t, &core.Translation{Items: tc.expected, Tokens: core.ModelTokens{Input: 34, Output: 2}}, trans, "translation mismatch for output %q", tc.output)
		assert.Equal(t, 1234, svc.maxTokens)
		assert.Contains(t, svc.instructions, `from the language with the ISO code "eng" to the language with the ISO code "spa"`)
	}

	// unknown source language uses different instructions
	svc := &promptService{output: `{"a:text": ["Hola"]}`}
	_, err := ai.TranslateByPrompt(ctx, svc, "und", "spa", map[string][]string{"a:text": {"Hello"}}, 1000)
	assert.NoError(t, err)
	assert.NotContains(t, svc.instructions, `"und"`)

	svc = &promptService{err: errors.New("boom")}
	trans, err := ai.TranslateByPrompt(ctx, svc, "eng", "spa", items, 1000)
	assert.EqualError(t, err, "boom")
	assert.Nil(t, trans)
}
