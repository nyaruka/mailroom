package ai_test

import (
	"context"
	"testing"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/test/services"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/stretchr/testify/assert"
)

func TestTranslate(t *testing.T) {
	ctx := context.Background()

	items := map[string][]string{
		"a:text":          {`Hi @contact.name, your total is @(fields.total * 2) - email bob@gmail.com or pay @@contact`},
		"a:quick_replies": {"Yes", "@fields.other"},
		"b:text":          {"@contact.name @(fields.age)"}, // nothing to translate
		"c:text":          {"See @results.name.value."},
		"d:text":          {"Oops @(upper(contact.name"}, // unterminated expression
		"e:text":          {"Hi @contact.name"},
	}

	svc := services.NewMockModel(&services.MockModelResult{
		Items: map[string][]string{
			"a:text":          {`Hola <x id="1"/>, tu total es <x id="2"/> - escribe a bob@gmail.com o paga @contact`},
			"a:quick_replies": {"Sí", `<x id="1"/>`},
			"c:text":          {`Ver <x id="2"/>.`}, // wrong placeholder id
			"d:text":          {`Uy <x id="1"/>`},
			"e:text":          {`Hola <x id="1"/> y <x id="1"/>`}, // duplicated placeholder
		},
		Tokens: core.ModelTokens{Input: 12, Output: 34},
	})

	trans, err := ai.Translate(ctx, svc, "eng", "spa", items)
	assert.NoError(t, err)
	assert.Equal(t, &core.Translation{
		Items: map[string][]string{
			"a:text":          {`Hola @contact.name, tu total es @(fields.total * 2) - escribe a bob@gmail.com o paga @@contact`},
			"a:quick_replies": {"Sí", "@fields.other"},
			"b:text":          {"@contact.name @(fields.age)"},
			"d:text":          {"Uy @(upper(contact.name"},
		},
		Tokens: core.ModelTokens{Input: 12, Output: 34},
	}, trans)

	// expressions were masked and items without text weren't sent
	assert.Equal(t, map[string][]string{
		"a:text":          {`Hi <x id="1"/>, your total is <x id="2"/> - email bob@gmail.com or pay @contact`},
		"a:quick_replies": {"Yes", `<x id="1"/>`},
		"c:text":          {`See <x id="1"/>.`},
		"d:text":          {`Oops <x id="1"/>`},
		"e:text":          {`Hi <x id="1"/>`},
	}, svc.Calls()[0].Items)

	// nothing to translate means no call to the service
	svc = services.NewMockModel()
	trans, err = ai.Translate(ctx, svc, "eng", "spa", map[string][]string{"a:text": {"@contact.name!"}})
	assert.NoError(t, err)
	assert.Equal(t, &core.Translation{Items: map[string][]string{"a:text": {"@contact.name!"}}}, trans)
	assert.Len(t, svc.Calls(), 0)

	// errors from the service are returned
	svc = services.NewMockModel(&services.MockModelResult{Error: "boom"})
	_, err = ai.Translate(ctx, svc, "eng", "spa", map[string][]string{"a:text": {"Hello"}})
	assert.EqualError(t, err, "boom")
}
