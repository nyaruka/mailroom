package ai

import (
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/excellent"
	"github.com/nyaruka/goflow/flows"
)

// TranslateMasked translates items with the given function after replacing their expressions with placeholders of the
// given syntax, so that the translator never has to recognize or preserve expressions itself. Items which can't be
// masked, or whose translations don't keep each of their placeholders exactly once, are omitted from the result.
func TranslateMasked(items map[string][]string, syntax *excellent.PlaceholderSyntax, translate func(map[string][]string) (*core.Translation, error)) (*core.Translation, error) {
	masked := make(map[string][]*excellent.MaskedTemplate, len(items))
	texts := make(map[string][]string, len(items))

	for id, vals := range items {
		if ms := maskStrings(vals, syntax); ms != nil {
			masked[id] = ms
			texts[id] = make([]string, len(ms))
			for i, m := range ms {
				texts[id][i] = m.Text
			}
		}
	}

	result := make(map[string][]string, len(texts))
	if len(texts) == 0 {
		return &core.Translation{Items: result}, nil
	}

	trans, err := translate(texts)
	if err != nil {
		return nil, err
	}

	for id, tvals := range trans.Items {
		if ms := masked[id]; len(tvals) == len(ms) {
			if unmasked := unmaskStrings(ms, tvals); unmasked != nil {
				result[id] = unmasked
			}
		}
	}

	return &core.Translation{Items: result, Tokens: trans.Tokens}, nil
}

// HasTranslatableText returns whether any of the given strings has text outside of its expressions
func HasTranslatableText(vals []string) bool {
	for _, v := range vals {
		// the syntax doesn't matter here, and a string which can't be masked is assumed to have text
		if m, err := excellent.MaskTemplate(v, flows.RunContextTopLevels, excellent.BracePlaceholders); err != nil || m.HasText() {
			return true
		}
	}
	return false
}

func maskStrings(vals []string, syntax *excellent.PlaceholderSyntax) []*excellent.MaskedTemplate {
	ms := make([]*excellent.MaskedTemplate, len(vals))
	for i, v := range vals {
		m, err := excellent.MaskTemplate(v, flows.RunContextTopLevels, syntax)
		if err != nil {
			return nil
		}
		ms[i] = m
	}
	return ms
}

func unmaskStrings(ms []*excellent.MaskedTemplate, tvals []string) []string {
	unmasked := make([]string, len(tvals))
	for i, tv := range tvals {
		u, err := ms[i].Unmask(tv)
		if err != nil {
			return nil
		}
		unmasked[i] = u
	}
	return unmasked
}
