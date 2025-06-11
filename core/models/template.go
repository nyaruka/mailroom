package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/assets/static"
	"github.com/nyaruka/null/v3"
)

// TemplateID is our type for the database id of a template
type TemplateID null.Int

const NilTemplateID = TemplateID(0)

type Template struct {
	ID_           TemplateID             `json:"id"`
	UUID_         assets.TemplateUUID    `json:"uuid"`
	Name_         string                 `json:"name"`
	Translations_ []*TemplateTranslation `json:"translations"`

	translations []assets.TemplateTranslation
}

func (t *Template) ID() TemplateID                             { return t.ID_ }
func (t *Template) UUID() assets.TemplateUUID                  { return t.UUID_ }
func (t *Template) Name() string                               { return t.Name_ }
func (t *Template) Translations() []assets.TemplateTranslation { return t.translations }

func (t *Template) FindTranslation(ch *Channel, l i18n.Locale) *TemplateTranslation {
	for _, tt := range t.Translations_ {
		if tt.Channel().UUID == ch.UUID() && tt.Locale() == l {
			return tt
		}
	}
	return nil
}

func (t *Template) UnmarshalJSON(d []byte) error {
	type T Template // need to alias type to avoid circular calls to this method

	if err := json.Unmarshal(d, (*T)(t)); err != nil {
		return err
	}

	t.translations = make([]assets.TemplateTranslation, len(t.Translations_))
	for i := range t.Translations_ {
		t.translations[i] = t.Translations_[i]
	}
	return nil
}

type TemplateTranslation struct {
	t struct {
		Channel        *assets.ChannelReference    `json:"channel"`
		Namespace      string                      `json:"namespace"`
		ExternalID     string                      `json:"external_id"`
		Locale         i18n.Locale                 `json:"locale"`
		ExternalLocale string                      `json:"external_locale"`
		Components     []*static.TemplateComponent `json:"components"`
		Variables      []*static.TemplateVariable  `json:"variables"`
	}

	components []assets.TemplateComponent
	variables  []assets.TemplateVariable
}

func (t *TemplateTranslation) Channel() *assets.ChannelReference      { return t.t.Channel }
func (t *TemplateTranslation) Namespace() string                      { return t.t.Namespace }
func (t *TemplateTranslation) ExternalID() string                     { return t.t.ExternalID }
func (t *TemplateTranslation) Locale() i18n.Locale                    { return t.t.Locale }
func (t *TemplateTranslation) ExternalLocale() string                 { return t.t.ExternalLocale }
func (t *TemplateTranslation) Components() []assets.TemplateComponent { return t.components }
func (t *TemplateTranslation) Variables() []assets.TemplateVariable   { return t.variables }

func (t *TemplateTranslation) UnmarshalJSON(d []byte) error {
	if err := json.Unmarshal(d, &t.t); err != nil {
		return err
	}

	t.components = make([]assets.TemplateComponent, len(t.t.Components))
	for i := range t.t.Components {
		t.components[i] = t.t.Components[i]
	}

	t.variables = make([]assets.TemplateVariable, len(t.t.Variables))
	for i := range t.t.Variables {
		t.variables[i] = t.t.Variables[i]
	}

	return nil
}

// loads the templates for the passed in org
func loadTemplates(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.Template, error) {
	rows, err := db.QueryContext(ctx, sqlSelectTemplatesByOrg, orgID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("error querying templates for org: %d: %w", orgID, err)
	}
	return ScanJSONRows(rows, func() assets.Template { return &Template{} })
}

const sqlSelectTemplatesByOrg = `
SELECT ROW_TO_JSON(r) FROM (
     SELECT t.id, t.uuid, t.name, (SELECT ARRAY_TO_JSON(ARRAY_AGG(ROW_TO_JSON(tr))) FROM (
         SELECT JSON_BUILD_OBJECT('uuid', c.uuid, 'name', c.name) as channel, tr.locale, tr.components, tr.variables, tr.external_id, tr.external_locale, tr.namespace
           FROM templates_templatetranslation tr
           JOIN channels_channel c ON tr.channel_id = c.id
          WHERE tr.template_id = t.id AND c.is_active = TRUE AND tr.status = 'A' AND tr.is_supported AND tr.is_compatible
         ) tr) as translations
       FROM templates_template t
      WHERE org_id = $1 
   ORDER BY name ASC
) r;`

func (i *TemplateID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i TemplateID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *TemplateID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i TemplateID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
