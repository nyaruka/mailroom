package ai_test

import (
	"errors"
	"testing"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/excellent"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/stretchr/testify/assert"
)

func TestTranslateMasked(t *testing.T) {
	items := map[string][]string{
		"a:text":          {"Hi @contact.name, email bob@gmail.com"},
		"a:quick_replies": {"Yes", "@fields.other"},
		"b:text":          {"See @results.name.value."},
		"c:text":          {"Hi @contact.name"},
		"d:text":          {`Already has <x id="1"/>`}, // can't be masked
	}

	var sent map[string][]string
	trans, err := ai.TranslateMasked(items, excellent.TagPlaceholders, func(masked map[string][]string) (*core.Translation, error) {
		sent = masked
		return &core.Translation{
			Items: map[string][]string{
				"a:text":          {`Hola <x id="1"/>, escribe a bob@gmail.com`},
				"a:quick_replies": {"Sí", `<x id="1"/>`},
				"b:text":          {`Ver <x id="2"/>.`},               // wrong placeholder
				"c:text":          {`Hola <x id="1"/> y <x id="1"/>`}, // duplicated placeholder
			},
			Tokens: core.ModelTokens{Input: 12, Output: 34},
		}, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, map[string][]string{
		"a:text":          {`Hi <x id="1"/>, email bob@gmail.com`},
		"a:quick_replies": {"Yes", `<x id="1"/>`},
		"b:text":          {`See <x id="1"/>.`},
		"c:text":          {`Hi <x id="1"/>`},
	}, sent)
	assert.Equal(t, &core.Translation{
		Items: map[string][]string{
			"a:text":          {"Hola @contact.name, escribe a bob@gmail.com"},
			"a:quick_replies": {"Sí", "@fields.other"},
		},
		Tokens: core.ModelTokens{Input: 12, Output: 34},
	}, trans)

	// nothing which can be masked means nothing to translate
	trans, err = ai.TranslateMasked(map[string][]string{"a:text": {`Has \{{1}}`}}, excellent.BracePlaceholders, func(map[string][]string) (*core.Translation, error) {
		panic("shouldn't be called")
	})
	assert.NoError(t, err)
	assert.Equal(t, &core.Translation{Items: map[string][]string{}}, trans)

	_, err = ai.TranslateMasked(items, excellent.TagPlaceholders, func(map[string][]string) (*core.Translation, error) {
		return nil, errors.New("boom")
	})
	assert.EqualError(t, err, "boom")
}

func TestHasTranslatableText(t *testing.T) {
	assert.True(t, ai.HasTranslatableText([]string{"Hello"}))
	assert.True(t, ai.HasTranslatableText([]string{"@contact.name", "Hi @contact.name"}))
	assert.True(t, ai.HasTranslatableText([]string{"bob@gmail.com"}))
	assert.False(t, ai.HasTranslatableText([]string{"@contact.name!", "@(fields.age) {{fields.city}}"}))
	assert.False(t, ai.HasTranslatableText([]string{}))
}
