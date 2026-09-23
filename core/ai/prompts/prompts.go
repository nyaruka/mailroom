package prompts

import (
	_ "embed"
	"strings"
	"text/template"

	"github.com/nyaruka/mailroom/v26/core/goflow"
)

//go:embed templates/categorize.txt
var categorize string

//go:embed templates/classify.txt
var classify string

//go:embed templates/translate.txt
var translate string

//go:embed templates/translate_unknown_from.txt
var translateUnknownFrom string

var templates = map[string]*template.Template{
	"categorize":             template.Must(template.New("").Parse(categorize)),
	"classify":               template.Must(template.New("").Parse(classify)),
	"translate":              template.Must(template.New("").Parse(translate)),
	"translate_unknown_from": template.Must(template.New("").Parse(translateUnknownFrom)),
}

func init() {
	goflow.RegisterLLMPrompts(templates)
}

// Render is a helper function to render a template with the given data.
func Render(template string, data any) string {
	tpl := templates[template]
	if tpl == nil {
		panic("no such prompt template: " + template)
	}
	var out strings.Builder
	if err := tpl.Execute(&out, data); err != nil {
		panic(err)
	}
	return out.String()
}
