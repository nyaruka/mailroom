package models_test

import (
	"testing"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/assets/static"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplates(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTemplates)
	require.NoError(t, err)

	templates, err := oa.Templates()
	require.NoError(t, err)

	assert.Equal(t, 2, len(templates))
	assert.Equal(t, assets.TemplateUUID("3b8dd151-1a91-411f-90cb-dd9065bb7a71"), templates[0].UUID())
	assert.Equal(t, "goodbye", templates[0].Name())
	assert.Equal(t, "revive_issue", templates[1].Name())

	assert.Equal(t, 1, len(templates[0].Translations()))
	tt := templates[0].Translations()[0]
	assert.Equal(t, i18n.Locale("fra"), tt.Locale())
	assert.Equal(t, "fr", tt.(*models.TemplateTranslation).ExternalLocale())
	assert.Equal(t, "", tt.(*models.TemplateTranslation).Namespace())
	assert.Equal(t, testdb.FacebookChannel.UUID, tt.Channel().UUID)
	assert.Equal(t, "Salut!", tt.Components()[0].Content())

	assert.Equal(t, 1, len(templates[1].Translations()))
	tt = templates[1].Translations()[0]

	tp1 := static.TemplateVariable{Type_: "text"}
	tp2 := static.TemplateVariable{Type_: "text"}

	assert.Equal(t, i18n.Locale("eng-US"), tt.Locale())
	assert.Equal(t, "en_US", tt.(*models.TemplateTranslation).ExternalLocale())
	assert.Equal(t, []assets.TemplateVariable{&tp1, &tp2}, tt.Variables())
	assert.Equal(t, "2d40b45c_25cd_4965_9019_f05d0124c5fa", tt.(*models.TemplateTranslation).Namespace())
	assert.Equal(t, testdb.FacebookChannel.UUID, tt.Channel().UUID)

	if assert.Len(t, tt.Components(), 1) {
		c1 := tt.Components()[0]
		assert.Equal(t, "body/text", c1.Type())
		assert.Equal(t, "body", c1.Name())
		assert.Equal(t, "Hi {{1}}, are you still experiencing problems with {{2}}?", c1.Content())
		assert.Equal(t, map[string]int{"1": 0, "2": 1}, c1.Variables())
	}

	fb := oa.ChannelByUUID(testdb.FacebookChannel.UUID)

	mt := oa.TemplateByUUID("3b8dd151-1a91-411f-90cb-dd9065bb7a71")
	assert.NotNil(t, mt)
	assert.NotNil(t, mt.FindTranslation(fb, "fra"))
	assert.Nil(t, mt.FindTranslation(fb, "eng"))

	assert.Nil(t, oa.TemplateByUUID("f67e498e-08fa-44e0-8acd-4c10122de714"))
}
