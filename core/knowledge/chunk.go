// Package knowledge implements the knowledge base feature - splitting source content into the chunks which get
// embedded and indexed, and searching over those.
package knowledge

import (
	"strings"
	"unicode/utf8"
)

const (
	chunkTargetSize = 1000 // the preferred maximum size of a chunk in runes
	chunkMaxSize    = 1500 // the hard maximum size of a chunk in runes
	chunkOverlap    = 150  // roughly how many trailing runes of a chunk to repeat at the start of the next
)

// the boundaries we prefer to split at, best first: paragraphs, lines, sentences, words
var chunkSeparators = []string{"\n\n", "\n", ". ", "! ", "? ", " "}

// ChunkText splits the given text into chunks for embedding - each at most chunkMaxSize runes and roughly
// chunkTargetSize, preferring to split at paragraph boundaries, then lines, sentences and words. A natural
// unit like a paragraph is kept whole if it fits within the hard maximum. Adjacent chunks built from small
// pieces share up to chunkOverlap trailing runes so that meaning isn't lost at a boundary.
func ChunkText(text string) []string {
	return mergePieces(splitPiece(text, 0))
}

// recursively splits text into pieces no larger than chunkMaxSize runes, splitting at the best boundary
// available and falling back to hard cuts for text with no boundaries at all
func splitPiece(text string, sepIdx int) []string {
	if utf8.RuneCountInString(text) <= chunkMaxSize {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []string{text}
	}
	if sepIdx >= len(chunkSeparators) {
		return hardCut(text)
	}

	pieces := make([]string, 0, 4)
	for _, part := range strings.SplitAfter(text, chunkSeparators[sepIdx]) {
		pieces = append(pieces, splitPiece(part, sepIdx+1)...)
	}
	return pieces
}

// cuts text into pieces of chunkTargetSize runes (the last one smaller)
func hardCut(text string) []string {
	runes := []rune(text)
	pieces := make([]string, 0, len(runes)/chunkTargetSize+1)
	for i := 0; i < len(runes); i += chunkTargetSize {
		pieces = append(pieces, string(runes[i:min(i+chunkTargetSize, len(runes))]))
	}
	return pieces
}

// merges pieces into chunks of roughly chunkTargetSize runes, carrying trailing pieces totalling at most
// chunkOverlap runes over into the next chunk as overlap
func mergePieces(pieces []string) []string {
	chunks := make([]string, 0, 4)
	window := make([]string, 0, 8) // the pieces making up the chunk being built
	total := 0                     // total runes in the window

	flush := func() {
		if chunk := strings.TrimSpace(strings.Join(window, "")); chunk != "" {
			chunks = append(chunks, chunk)
		}
	}

	for _, piece := range pieces {
		size := utf8.RuneCountInString(piece)
		if total > 0 && total+size > chunkTargetSize {
			flush()
			// keep trailing pieces as overlap for the next chunk, dropping more if needed to stay under
			// the hard maximum
			for len(window) > 0 && (total > chunkOverlap || total+size > chunkMaxSize) {
				total -= utf8.RuneCountInString(window[0])
				window = window[1:]
			}
		}
		window = append(window, piece)
		total += size
	}
	if total > 0 {
		flush()
	}
	return chunks
}
