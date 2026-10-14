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
		{output: ` "<CANT>"`, expected: map[string][]string{}},
		{output: "```json\n\"<CANT>\"\n```", expected: map[string][]string{}},
		{output: "```\n<CANT>\n```", expected: map[string][]string{}},
		{output: `not JSON`, expected: map[string][]string{}},
	}

	for _, tc := range tcs {
		svc := &promptService{output: tc.output}
		trans, err := ai.TranslateByPrompt(ctx, svc, "eng", "spa", items, 1234)
		assert.NoError(t, err)
		assert.Equal(t, &core.Translation{Items: tc.expected, Tokens: core.ModelTokens{Input: 34, Output: 2}}, trans, "translation mismatch for output %q", tc.output)
		assert.Equal(t, 1234, svc.maxTokens)
		assert.Contains(t, svc.instructions, `from English (ISO code "eng") to Spanish (ISO code "spa")`)
	}

	// less common languages are named too, and languages without a name are described by their code alone
	svc := &promptService{output: `{"a:text": ["Hola"]}`}
	_, err := ai.TranslateByPrompt(ctx, svc, "prd", "aar", map[string][]string{"a:text": {"Hello"}}, 1000)
	assert.NoError(t, err)
	assert.Contains(t, svc.instructions, `from the language with the ISO code "prd" to Afar (ISO code "aar")`)

	// unrecognized target languages aren't sent to the model
	svc = &promptService{output: `{"a:text": ["Hola"]}`}
	trans, err := ai.TranslateByPrompt(ctx, svc, "eng", "xxx", map[string][]string{"a:text": {"Hello"}}, 1000)
	assert.NoError(t, err)
	assert.Equal(t, &core.Translation{Items: map[string][]string{}}, trans)
	assert.Equal(t, "", svc.instructions)

	// as are codes which don't identify a particular language
	trans, err = ai.TranslateByPrompt(ctx, svc, "eng", "und", map[string][]string{"a:text": {"Hello"}}, 1000)
	assert.NoError(t, err)
	assert.Equal(t, &core.Translation{Items: map[string][]string{}}, trans)
	assert.Equal(t, "", svc.instructions)

	// unknown source language uses different instructions
	svc = &promptService{output: `{"a:text": ["Hola"]}`}
	_, err = ai.TranslateByPrompt(ctx, svc, "und", "spa", map[string][]string{"a:text": {"Hello"}}, 1000)
	assert.NoError(t, err)
	assert.NotContains(t, svc.instructions, `"und"`)
	assert.Contains(t, svc.instructions, `to Spanish (ISO code "spa")`)

	// expressions are sent to the model as placeholders and restored in its translations
	svc = &promptService{output: `{"a:text": ["Hola {{1}}, ¿{{2}}?"]}`}
	trans, err = ai.TranslateByPrompt(ctx, svc, "eng", "spa", map[string][]string{"a:text": {"Hi @contact.name, @(upper(fields.city))?"}}, 1000)
	assert.NoError(t, err)
	assert.Equal(t, map[string][]string{"a:text": {"Hola @contact.name, ¿@(upper(fields.city))?"}}, trans.Items)
	assert.JSONEq(t, `{"a:text": ["Hi {{1}}, {{2}}?"]}`, svc.input)
	assert.Contains(t, svc.instructions, `Placeholders like {{1}} stand for values inserted later.`)

	svc = &promptService{err: errors.New("boom")}
	trans, err = ai.TranslateByPrompt(ctx, svc, "eng", "spa", items, 1000)
	assert.EqualError(t, err, "boom")
	assert.Nil(t, trans)
}
