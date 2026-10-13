package ai

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/excellent"
	"github.com/nyaruka/goflow/flows"
)

// expressions are replaced by tags rather than some plain-text token because translation APIs have ways to protect
// markup from translation, and models reliably preserve it
var placeholderRegex = regexp.MustCompile(`<x id="(\d+)"\s*/>`)

func placeholder(n int) string { return fmt.Sprintf(`<x id="%d"/>`, n) }

// Translate translates items with the given service, replacing their expressions with placeholder tags first so that
// the service never has to recognize them. An item whose translation doesn't keep each of its placeholders exactly
// once is dropped, and an item with no text outside of its expressions is returned as it is.
func Translate(ctx context.Context, svc flows.ModelService, source, target i18n.Language, items map[string][]string) (*core.Translation, error) {
	result := make(map[string][]string, len(items))
	masked := make(map[string][]*maskedString, len(items))
	toTranslate := make(map[string][]string, len(items))

	for id, vals := range items {
		ms := make([]*maskedString, len(vals))
		texts := make([]string, len(vals))
		hasText := false
		for i, v := range vals {
			ms[i] = maskExpressions(v)
			texts[i] = ms[i].text
			hasText = hasText || ms[i].hasText()
		}
		if hasText {
			masked[id] = ms
			toTranslate[id] = texts
		} else {
			result[id] = vals
		}
	}

	if len(toTranslate) == 0 {
		return &core.Translation{Items: result}, nil
	}

	trans, err := svc.Translate(ctx, source, target, toTranslate)
	if err != nil {
		return trans, err
	}

	for id, tvals := range trans.Items {
		ms := masked[id]
		if len(tvals) != len(ms) {
			continue
		}
		unmasked := make([]string, len(tvals))
		for i, tv := range tvals {
			u, ok := ms[i].unmask(tv)
			if !ok {
				unmasked = nil
				break
			}
			unmasked[i] = u
		}
		if unmasked != nil {
			result[id] = unmasked
		}
	}

	return &core.Translation{Items: result, Tokens: trans.Tokens}, nil
}

// a template string with its expressions replaced by numbered placeholders
type maskedString struct {
	text  string   // unescaped text with placeholders, e.g. `Hi <x id="1"/>, your email is bob@gmail.com`
	exprs []string // source of each placeholder's expression, in order of id
}

func maskExpressions(s string) *maskedString {
	m := &maskedString{}
	var buf strings.Builder

	// scanned without unescaping so that a body starting with @( can only be an unterminated expression
	excellent.VisitTemplate(s, flows.RunContextTopLevels, false, func(tt excellent.XTokenType, token string) error {
		var expr string
		switch tt {
		case excellent.IDENTIFIER:
			expr = "@" + token
		case excellent.EXPRESSION:
			expr = "@(" + token + ")"
		default:
			if !strings.HasPrefix(token, "@(") {
				buf.WriteString(strings.ReplaceAll(token, "@@", "@"))
				return nil
			}
			expr = token // kept verbatim so that it fails the same way when evaluated
		}
		m.exprs = append(m.exprs, expr)
		buf.WriteString(placeholder(len(m.exprs)))
		return nil
	})

	m.text = buf.String()
	return m
}

// whether there is anything to translate outside of the placeholders
func (m *maskedString) hasText() bool {
	return strings.IndexFunc(placeholderRegex.ReplaceAllString(m.text, ""), func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsNumber(r)
	}) >= 0
}

// restores the expressions in a translation of this string's text, failing if it doesn't contain each placeholder
// exactly once
func (m *maskedString) unmask(translated string) (string, bool) {
	matches := placeholderRegex.FindAllStringSubmatchIndex(translated, -1)
	if len(matches) != len(m.exprs) {
		return "", false
	}

	var buf strings.Builder
	seen := make([]bool, len(m.exprs))
	last := 0
	for _, match := range matches {
		id, _ := strconv.Atoi(translated[match[2]:match[3]])
		if id < 1 || id > len(m.exprs) || seen[id-1] {
			return "", false
		}
		seen[id-1] = true
		buf.WriteString(escapeText(translated[last:match[0]]))
		buf.WriteString(m.exprs[id-1])
		last = match[1]
	}
	buf.WriteString(escapeText(translated[last:]))
	return buf.String(), true
}

// escapes each @ in translated text which would otherwise start an expression, leaving others, e.g. in an email
// address, as they are
func escapeText(s string) string {
	var buf strings.Builder
	for i, r := range s {
		buf.WriteRune(r)
		if r != '@' {
			continue
		}
		rest := s[i+1:]
		if strings.HasPrefix(rest, "@") || strings.HasPrefix(rest, "(") {
			buf.WriteRune('@')
			continue
		}
		end := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' })
		if end < 0 {
			end = len(rest)
		}
		if end > 0 && slices.Contains(flows.RunContextTopLevels, strings.ToLower(rest[:end])) {
			buf.WriteRune('@')
		}
	}
	return buf.String()
}
