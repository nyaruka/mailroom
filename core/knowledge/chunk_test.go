package knowledge_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkText(t *testing.T) {
	// empty and whitespace only text gives no chunks
	assert.Empty(t, knowledge.ChunkText(""))
	assert.Empty(t, knowledge.ChunkText("  \n\n\t "))

	// text within the hard maximum is a single trimmed chunk
	assert.Equal(t, []string{"Hello world."}, knowledge.ChunkText("Hello world.\n"))
	medium := strings.Repeat("alpha ", 200) + "omega" // 1205 runes, over target but under hard max
	assert.Equal(t, []string{medium}, knowledge.ChunkText(medium))

	// paragraphs are kept whole and packed into chunks up to the target size
	pa := strings.Repeat("alpha ", 66) + "alpha" // 401 runes each
	pb := strings.Repeat("bravo ", 66) + "bravo"
	pc := strings.Repeat("chess ", 66) + "chess"
	pd := strings.Repeat("delta ", 66) + "delta"
	chunks := knowledge.ChunkText(pa + "\n\n" + pb + "\n\n" + pc + "\n\n" + pd)
	assert.Equal(t, []string{pa + "\n\n" + pb, pc + "\n\n" + pd}, chunks)

	// a long paragraph is split at sentence boundaries, with sentence sized overlap between chunks
	sentence := func(i int) string { return fmt.Sprintf("s%02d %s. ", i, strings.Repeat("x", 93)) } // 99 runes
	b := &strings.Builder{}
	for i := 1; i <= 20; i++ {
		b.WriteString(sentence(i))
	}
	chunks = knowledge.ChunkText(b.String())
	require.Len(t, chunks, 3)
	assert.True(t, strings.HasPrefix(chunks[0], "s01 "))
	assert.True(t, strings.HasSuffix(chunks[0], "s10 "+strings.Repeat("x", 93)+"."))
	assert.True(t, strings.HasPrefix(chunks[1], "s10 ")) // s10 repeated as overlap
	assert.True(t, strings.HasSuffix(chunks[1], "s19 "+strings.Repeat("x", 93)+"."))
	assert.Equal(t, strings.TrimSpace(sentence(19)+sentence(20)), chunks[2]) // s19 repeated as overlap

	// text with no boundaries at all is hard cut, at rune boundaries
	chunks = knowledge.ChunkText(strings.Repeat("é", 3200))
	require.Len(t, chunks, 4)
	for i, c := range chunks {
		assert.True(t, utf8.ValidString(c))
		if i < 3 {
			assert.Equal(t, 1000, utf8.RuneCountInString(c))
		} else {
			assert.Equal(t, 200, utf8.RuneCountInString(c))
		}
	}

	// no chunk ever exceeds the hard maximum, and all content is retained
	b.Reset()
	for i := range 100 {
		b.WriteString(fmt.Sprintf("Sentence number %d ends here. ", i))
		if i%7 == 0 {
			b.WriteString("\n\n")
		}
	}
	chunks = knowledge.ChunkText(b.String())
	assert.Greater(t, len(chunks), 1)
	joined := strings.Join(chunks, " ")
	for _, c := range chunks {
		assert.LessOrEqual(t, utf8.RuneCountInString(c), 1500)
	}
	for i := range 100 {
		assert.Contains(t, joined, fmt.Sprintf("Sentence number %d ends here.", i))
	}
}

func TestChunkMarkdown(t *testing.T) {
	tcs := []struct {
		name     string
		md       string
		expected []string
	}{
		{
			name:     "empty",
			md:       "",
			expected: []string{},
		},
		{
			name:     "headings with no content",
			md:       "  \n\n# Billing\n\n## Refunds\n   ",
			expected: []string{},
		},
		{
			name:     "no headings",
			md:       "Refunds take 5 days.\n\nUnless it's a weekend.\n",
			expected: []string{"Refunds take 5 days.\n\nUnless it's a weekend."},
		},
		{
			name: "content before the first heading has no path",
			md:   "Everything you need to know.\n\n# Billing\n\nHow billing works.\n\n## Refunds\n\nRefunds take 5 days.",
			expected: []string{
				"Everything you need to know.\n\nBilling\n\nHow billing works.\n\nBilling > Refunds\n\nRefunds take 5 days.",
			},
		},
		{
			name: "heading path pops back to the new level",
			md:   "# Billing\n\naaa\n\n## Refunds\n\nbbb\n\n### Timing\n\nccc\n\n## Invoices\n\nddd\n\n# Shipping\n\neee",
			expected: []string{
				"Billing\n\naaa\n\nBilling > Refunds\n\nbbb\n\nBilling > Refunds > Timing\n\nccc\n\n" +
					"Billing > Invoices\n\nddd\n\nShipping\n\neee",
			},
		},
		{
			name:     "a heading with no body still contributes to the path",
			md:       "# Billing\n## Refunds\nRefunds take 5 days.",
			expected: []string{"Billing > Refunds\n\nRefunds take 5 days."},
		},
		{
			name:     "closing hashes aren't part of the heading",
			md:       "## Refunds ##\n\nRefunds take 5 days.",
			expected: []string{"Refunds\n\nRefunds take 5 days."},
		},
		{
			name:     "a hash without a space isn't a heading",
			md:       "#NotAHeading\n\nSee also #tags.",
			expected: []string{"#NotAHeading\n\nSee also #tags."},
		},
		{
			name:     "setext headings are ignored",
			md:       "Refunds\n=======\n\nRefunds take 5 days.",
			expected: []string{"Refunds\n=======\n\nRefunds take 5 days."},
		},
		{
			name:     "images are stripped and links keep only their text",
			md:       "# Setup\n\nClick the [settings link](https://app.example.com/settings) to begin.\n\n![the settings screen](https://cdn.example.com/shot.png)\n\nThen save.",
			expected: []string{"Setup\n\nClick the settings link to begin.\n\nThen save."},
		},
		{
			name:     "an image inside a link is stripped with it",
			md:       "See [![screenshot](https://cdn.example.com/shot.png)](https://app.example.com) for details.",
			expected: []string{"See  for details."},
		},
		{
			name: "fenced code is literal - no headings, no stripping inside it",
			md:   "# Install\n\nRun this:\n\n```sh\n# install the tool\n\nnpm install [thing](https://x)\n```\n\nDone.",
			expected: []string{
				"Install\n\nRun this:\n\n```sh\n# install the tool\n\nnpm install [thing](https://x)\n```\n\nDone.",
			},
		},
		{
			name:     "an unclosed fence swallows the rest of the document",
			md:       "# Install\n\n~~~\n# not a heading\n",
			expected: []string{"Install\n\n~~~\n# not a heading"},
		},
	}

	for _, tc := range tcs {
		assert.Equal(t, tc.expected, knowledge.ChunkMarkdown(tc.md), "chunks mismatch for test '%s'", tc.name)
	}

	// sections big enough to stand alone become chunks of their own
	pa := strings.Repeat("alpha ", 100) + "alpha" // 601 runes each
	pb := strings.Repeat("bravo ", 100) + "bravo"
	assert.Equal(t,
		[]string{"Billing\n\n" + pa, "Billing > Refunds\n\n" + pb},
		knowledge.ChunkMarkdown("# Billing\n\n"+pa+"\n\n## Refunds\n\n"+pb),
	)

	// but a section too small to stand alone is merged into the one before it..
	assert.Equal(t,
		[]string{"Billing\n\n" + pa + "\n\nBilling > Refunds\n\nWithin 30 days."},
		knowledge.ChunkMarkdown("# Billing\n\n"+pa+"\n\n## Refunds\n\nWithin 30 days."),
	)

	// ..unless merging it would take the chunk over the hard maximum
	pc := strings.Repeat("chess ", 233) + "x" // 1399 runes
	pd := strings.Repeat("d", 200)
	assert.Equal(t,
		[]string{"Billing\n\n" + pc, "Billing > Refunds\n\n" + pd},
		knowledge.ChunkMarkdown("# Billing\n\n"+pc+"\n\n## Refunds\n\n"+pd),
	)

	// a section over the hard maximum is handed to the character splitter, each piece keeping the heading path
	long := strings.TrimSpace(strings.Repeat("alpha ", 300)) // 1799 runes
	pieces := knowledge.ChunkText(long)
	require.Len(t, pieces, 2)
	expected := make([]string, len(pieces))
	for i, p := range pieces {
		expected[i] = "Billing > Refunds\n\n" + p
	}
	assert.Equal(t, expected, knowledge.ChunkMarkdown("# Billing\n\n## Refunds\n\n"+long))

	// with no headings to go on it falls back to exactly what the character splitter would do
	assert.Equal(t, knowledge.ChunkText(long), knowledge.ChunkMarkdown(long))
}
