package knowledge

import (
	"regexp"
	"strings"
)

var mdBackticks = regexp.MustCompile("`+")

// StripComments removes the notes authors leave for each other in an article's markdown, which are for whoever edits
// it and never part of what it says. A note opens with /* at the start of a line and runs to the next */, across lines
// if need be - anything after the */ on its line is kept. Markers inside code - fenced or indented blocks, or inline
// spans - are code. A /* that's never closed before the next code fence isn't a note, so that a stray one can't
// swallow the rest of an article, or be closed by a */ in its code.
func StripComments(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))

	fence := ""                            // the marker of the code fence we're inside, if any
	span := ""                             // the backticks of an inline code span the last line left open, if any
	prevBlank, prevIndented := true, false // whether the last line kept was blank, or a line of an indented code block

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimLeft(line, " \t")
		blank := trimmed == ""

		// fences are block structure, so they take precedence over an inline code span
		if m := mdFence.FindStringSubmatch(line); m != nil && (fence == "" || m[1] == fence) {
			if fence == "" {
				fence = m[1]
			} else {
				fence = ""
			}
			out = append(out, line)
			span, prevBlank, prevIndented = "", false, false
			continue
		}

		// an indented code block can't interrupt a paragraph, so a line is one only after a blank line or another
		indented := !blank && isIndentedCode(line) && (prevBlank || prevIndented)

		if fence != "" || indented || span != "" || blank {
			out = append(out, line)
			if blank {
				span = "" // a code span can't cross a paragraph
			} else if fence == "" && !indented {
				span = openSpan(line, span)
			}
			prevBlank, prevIndented = blank, indented
			continue
		}

		if strings.HasPrefix(trimmed, "/*") {
			if end, rest, ok := commentEnd(lines, i, len(line)-len(trimmed)+2); ok {
				if strings.TrimSpace(rest) != "" {
					// what follows the */ is processed as a line of its own, since it can hold another marker
					lines[end] = strings.TrimLeft(rest, " \t")
					end--
				}
				i = end
				continue
			}
		}

		out = append(out, line)
		span = openSpan(line, "")
		prevBlank, prevIndented = false, false
	}

	return strings.Join(out, "\n")
}

// whether a line is indented far enough to be code: four spaces, or a tab
func isIndentedCode(line string) bool {
	return strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
}

// finds the */ closing a comment opened on line i just before the given byte offset - returning the index of the line
// it's on and what follows it there - or reports that it's never closed before the next code fence
func commentEnd(lines []string, i, from int) (int, string, bool) {
	for j := i; j < len(lines); j++ {
		text := lines[j]
		if j == i {
			text = text[from:]
		} else if mdFence.MatchString(text) {
			break
		}
		if k := strings.Index(text, "*/"); k >= 0 {
			return j, text[k+2:], true
		}
	}
	return 0, "", false
}

// the backticks of an inline code span a line leaves open, given those of one it started inside of - a span closes
// at the next run of exactly as many backticks as opened it
func openSpan(line, open string) string {
	for _, run := range mdBackticks.FindAllString(line, -1) {
		if open == "" {
			open = run
		} else if run == open {
			open = ""
		}
	}
	return open
}
