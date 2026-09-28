package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
)

// TranslateByPrompt translates items by prompting the given service to translate them as a JSON object, for model
// types which don't have a native translation API. Items the model can't translate are omitted from the result.
func TranslateByPrompt(ctx context.Context, svc flows.ModelService, source, target i18n.Language, items map[string][]string, maxTokens int) (*core.Translation, error) {
	instructionsTpl := "translate"
	if source == "und" || source == "mul" {
		instructionsTpl = "translate_unknown_from"
	}
	instructions := prompts.Render(instructionsTpl, map[string]any{"Source": source, "Target": target})

	input, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("error marshaling input: %w", err)
	}

	resp, err := svc.Response(ctx, instructions, string(input), maxTokens)
	if err != nil {
		return nil, err
	}

	return &core.Translation{Items: parseTranslated(resp.Output, items), Tokens: resp.Tokens}, nil
}

// parses the output of the translate instructions. A <CANT> or anything unparseable means nothing was translatable.
// The model can also signal that an item is untranslatable by returning <CANT> in place of any of its strings or by
// omitting it, either of which drops that item.
func parseTranslated(output string, items map[string][]string) map[string][]string {
	translatable := make(map[string][]string)
	if output == cantOutput {
		return translatable
	}

	var translated map[string][]string
	if err := json.Unmarshal([]byte(output), &translated); err != nil {
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
