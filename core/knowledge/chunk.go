// Package knowledge implements the knowledge base feature - splitting source content into the chunks which get
// embedded and indexed, and searching over those.
package knowledge

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	chunkTargetSize = 1000 // the preferred maximum size of a chunk in runes
	chunkMaxSize    = 1500 // the hard maximum size of a chunk in runes
	chunkOverlap    = 150  // roughly how many trailing runes of a chunk to repeat at the start of the next
	chunkMergeSize  = 333  // roughly a third of the target - the size below which a markdown section isn't a chunk
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

const headingSep = " > " // joins a section's heading path, e.g. "Billing > Refunds"

var (
	// ATX headings only - a setext heading (underlined with = or -) is rare in authored help content and telling one
	// from a table row or a horizontal rule needs a real parser
	mdHeading = regexp.MustCompile(`^ {0,3}(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)
	mdFence   = regexp.MustCompile("^ {0,3}(```|~~~)")
	mdImage   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
)

// ChunkMarkdown splits authored markdown into chunks for embedding. Unlike arbitrary text, markdown tells us where
// the author thought one idea ended and the next began, so headings - not rune counts - decide the boundaries, and
// each chunk is prefixed with the path of headings above it ("Billing > Refunds") so that the section it came from is
// both embedded with it and visible in whatever a citation of it shows.
//
// Sections that are too small to stand alone are merged into the chunk before them, and ones too big for a single
// chunk are handed to ChunkText - deciding where prose can safely be cut stays in one implementation, and markdown
// mode only decides where the natural boundaries are. Markdown with no headings at all is therefore just chunked as
// text.
func ChunkMarkdown(md string) []string {
	chunks := make([]string, 0, 4)

	pending, pendingSize := "", 0 // the chunk being built, and its size in runes

	flush := func() {
		if pending != "" {
			chunks = append(chunks, pending)
		}
		pending, pendingSize = "", 0
	}

	for _, sec := range markdownSections(md) {
		body := strings.TrimSpace(sec.body)
		bodySize := utf8.RuneCountInString(body)

		if bodySize > chunkMaxSize {
			flush()
			for _, piece := range ChunkText(body) {
				chunks = append(chunks, withHeadingPath(sec.path, piece))
			}
			continue
		}

		text := withHeadingPath(sec.path, body)
		size := utf8.RuneCountInString(text)

		// merge a small section into the chunk being built rather than emitting it alone - an article that's a list of
		// one line headings would otherwise become dozens of chunks each too thin to retrieve on. The same applies
		// when it's the chunk being built that's small, so that a stub section doesn't strand one either.
		if pending != "" && (bodySize < chunkMergeSize || pendingSize < chunkMergeSize) && pendingSize+size+2 <= chunkMaxSize {
			pending += "\n\n" + text
			pendingSize += size + 2
			continue
		}

		flush()
		pending, pendingSize = text, size
	}
	flush()

	return chunks
}

// a run of markdown content under a heading, along with the path of headings it sits under
type markdownSection struct {
	path string // empty for the content before the first heading
	body string
}

// splits markdown into its sections, stripping inline noise as it goes
func markdownSections(md string) []markdownSection {
	sections := make([]markdownSection, 0, 8)
	current := markdownSection{}
	body := &strings.Builder{}
	path := make([]string, 0, 6) // the heading at each level above us
	fence := ""                  // the marker of the code fence we're inside, if any
	lastBlank := true            // whether the last line written was blank, so runs of them can be collapsed

	flush := func() {
		if strings.TrimSpace(body.String()) != "" {
			current.body = body.String()
			sections = append(sections, current)
		}
		body.Reset()
		lastBlank = true
	}

	for _, line := range strings.Split(md, "\n") {
		if m := mdFence.FindStringSubmatch(line); m != nil && (fence == "" || m[1] == fence) {
			if fence == "" {
				fence = m[1]
			} else {
				fence = ""
			}
			body.WriteString(line + "\n")
			lastBlank = false
			continue
		}

		// inside a fenced code block everything is literal - a # there is a comment or a shell prompt, and treating it
		// as a heading would cut a code sample in half and invent a section title from it. Blank lines are part of the
		// sample too, so they're kept as they are.
		if fence != "" {
			body.WriteString(line + "\n")
			lastBlank = false
			continue
		}

		if m := mdHeading.FindStringSubmatch(line); m != nil {
			flush()

			level := len(m[1])
			path = append(path[:min(level-1, len(path))], stripInline(strings.TrimSpace(m[2])))
			current = markdownSection{path: strings.Join(path, headingSep)}
			continue
		}

		// one blank line is a paragraph break and ChunkText splits on those, but more than one is just whitespace -
		// and stripping an image off its own line leaves exactly that
		stripped := stripInline(line)
		if strings.TrimSpace(stripped) == "" {
			if lastBlank {
				continue
			}
			lastBlank = true
			body.WriteString("\n")
			continue
		}

		lastBlank = false
		body.WriteString(stripped + "\n")
	}
	flush()

	return sections
}

// strips the inline markup that would only dilute an embedding: images go entirely, since the URL of a screenshot
// says nothing about what the article means, while links keep their text and lose their target
func stripInline(line string) string {
	line = mdImage.ReplaceAllString(line, "") // before links, or an image would leave its alt text behind
	return mdLink.ReplaceAllString(line, "$1")
}

// prefixes a chunk of a section with its heading path. Content above the first heading has no path and so gets no
// prefix. Note this can take a chunk slightly over the hard maximum - a heading path is small next to what it
// prefixes, and losing it would cost more in retrieval than the overshoot costs in truncation.
func withHeadingPath(path, text string) string {
	if path == "" {
		return text
	}
	return path + "\n\n" + text
}
