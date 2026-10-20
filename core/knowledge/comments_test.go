package knowledge_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/stretchr/testify/assert"
)

func TestStripComments(t *testing.T) {
	tcs := []struct {
		md       string
		stripped string
	}{
		// nothing to strip
		{"", ""},
		{"Just text.\n\nAnd more.", "Just text.\n\nAnd more."},

		// // isn't a note marker
		{"One\n// not a note\nTwo", "One\n// not a note\nTwo"},

		// a /* */ note goes, on one line or across several, along with the lines it fills
		{"One\n\n/* a note */\n\nTwo", "One\n\n\nTwo"},
		{"One\n/* a note\nthat goes on\n*/\nTwo", "One\nTwo"},
		{"  /* indented */\nTwo", "Two"},
		{"- item\n   /* about the item */\n- next", "- item\n- next"},

		// with what follows its */ on the line kept
		{"/* a note */ Then text.", "Then text."},
		{"/* a note\nstill */ Then text.\nMore", "Then text.\nMore"},

		// and checked for markers of its own
		{"/* a */ /* b */ text", "text"},
		{"/* a note\n*/ ```\n/* code */\n```", "```\n/* code */\n```"},

		// a /* partway through a line isn't a note, nor is a lone */
		{"Use a/*b*/c", "Use a/*b*/c"},
		{"Type the **/start** command.", "Type the **/start** command."},

		// and an unclosed /* isn't one either, so it can't swallow what follows
		{"One\n/* never closed\nTwo", "One\n/* never closed\nTwo"},

		// nor is one only closed by a */ past a code fence, which would be code
		{
			"/* draft\n\nIntro\n\n```\nls src/**/*.go\n```\n/* note */",
			"/* draft\n\nIntro\n\n```\nls src/**/*.go\n```",
		},

		// markers in a fenced code block are code
		{"```\n/* code */\n```\n/* note */", "```\n/* code */\n```"},
		{"~~~js\n/* code */\n~~~\nText", "~~~js\n/* code */\n~~~\nText"},
		{"```\n~~~\n/* code */\n```", "```\n~~~\n/* code */\n```"},

		// even when a stray backtick before it would otherwise have opened an inline span
		{"Press the ` key\n```\n/* code */\n```\n/* note */", "Press the ` key\n```\n/* code */\n```"},

		// as are those in an indented code block, which needs a blank line before it
		{
			"Text\n\n    /* code */\n\n    more code\n/* note */",
			"Text\n\n    /* code */\n\n    more code",
		},
		{"\t/* code */", "\t/* code */"},

		// and those in an inline code span, even one carried over from the line before
		{"Run `x\n/* not a note */` now", "Run `x\n/* not a note */` now"},
		{"Run ``a ` b\n/* not a note */`` now", "Run ``a ` b\n/* not a note */`` now"},
		{"Run `x` then\n/* a note */", "Run `x` then"},

		// a span can't cross a paragraph, so a backtick left open doesn't protect what follows a blank line
		{"Stray ` tick\n\n/* a note */", "Stray ` tick\n"},
	}

	for _, tc := range tcs {
		assert.Equal(t, tc.stripped, knowledge.StripComments(tc.md), "stripping %q", tc.md)
	}
}
