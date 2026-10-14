package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/excellent"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

var languageNames = display.English.Languages()

// special ISO 639 codes which don't identify a particular language
var nonLanguages = []i18n.Language{"und", "mul", "zxx", "mis"}

// TranslateByPrompt translates items by prompting the given service to translate them as a JSON object, for model
// types which don't have a native translation API. Items the model can't translate are omitted from the result.
func TranslateByPrompt(ctx context.Context, svc flows.ModelService, source, target i18n.Language, items map[string][]string, maxTokens int) (*core.Translation, error) {
	// given an unrecognized code, or one which isn't a particular language, models tend to guess at a language rather
	// than say they can't translate to it
	if _, err := language.ParseBase(string(target)); err != nil || slices.Contains(nonLanguages, target) {
		return &core.Translation{Items: map[string][]string{}}, nil
	}

	instructionsTpl := "translate"
	if source == "und" || source == "mul" {
		instructionsTpl = "translate_unknown_from"
	}
	instructions := prompts.Render(instructionsTpl, map[string]any{"Source": describeLanguage(source), "Target": describeLanguage(target)})

	return TranslateMasked(items, excellent.BracePlaceholders, func(masked map[string][]string) (*core.Translation, error) {
		input, err := json.Marshal(masked)
		if err != nil {
			return nil, fmt.Errorf("error marshaling input: %w", err)
		}

		resp, err := svc.Response(ctx, instructions, string(input), maxTokens)
		if err != nil {
			return nil, err
		}

		return &core.Translation{Items: parseTranslated(resp.Output, masked), Tokens: resp.Tokens}, nil
	})
}

// describes a language for a prompt by its English name as well as its code, since a model may not recognize a
// less common language from its code alone
func describeLanguage(lang i18n.Language) string {
	if base, err := language.ParseBase(string(lang)); err == nil {
		if name := languageNames.Name(base); name != "" {
			return fmt.Sprintf(`%s (ISO code "%s")`, name, lang)
		}
	}
	return fmt.Sprintf(`the language with the ISO code "%s"`, lang)
}

// parses the output of the translate instructions. A <CANT> or anything unparseable means nothing was translatable.
// The model can also signal that an item is untranslatable by returning <CANT> in place of any of its strings or by
// omitting it, either of which drops that item.
func parseTranslated(output string, items map[string][]string) map[string][]string {
	translatable := make(map[string][]string)
	if isCant(output) {
		return translatable
	}

	var translated map[string][]string
	if err := json.Unmarshal([]byte(jsonObject(output)), &translated); err != nil {
		slog.Warn("failed to parse translate output", "error", err, "output", output)
		return translatable
	}

	for id, vals := range items {
		tvals, ok := translated[id]
		if !ok || len(tvals) != len(vals) || slices.Contains(tvals, cantOutput) {
			continue
		}
		translatable[id] = tvals
	}
	return translatable
}
