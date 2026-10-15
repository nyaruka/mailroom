package ai

import (
	"encoding/json"
	"regexp"
	"strings"
)

// helpers for reading the output of a generative model

// the model's reply to instructions when it can't do what's asked
const cantOutput = "<CANT>"

var codeFenceRegex = regexp.MustCompile("(?s)^```(?:[\\w+-]*\\n)?(.*?)\\n?```$")

// StripFence strips surrounding whitespace and any code fence enclosing the output, as models sometimes fence even
// plain replies
func StripFence(output string) string {
	output = strings.TrimSpace(output)
	if m := codeFenceRegex.FindStringSubmatch(output); m != nil {
		return strings.TrimSpace(m[1])
	}
	return output
}

// whether the output is <CANT>, allowing for it being quoted or fenced
func isCant(output string) bool { return normalizeOption(output) == cantOutput }

// extracts the JSON object from model output, as models sometimes wrap JSON in code fences or other text, which can
// itself contain braces, e.g. when quoting the input
func jsonObject(output string) string {
	for i := strings.Index(output, "{"); i >= 0; {
		var obj json.RawMessage
		if err := json.NewDecoder(strings.NewReader(output[i:])).Decode(&obj); err == nil && obj[0] == '{' {
			return string(obj)
		}

		next := strings.Index(output[i+1:], "{")
		if next < 0 {
			break
		}
		i += next + 1
	}
	return ""
}
