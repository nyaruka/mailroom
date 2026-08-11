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
