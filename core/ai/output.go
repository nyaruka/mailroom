package ai

import (
	"regexp"
	"strings"
)

// helpers for reading the output of a generative model

// the model's reply to instructions when it can't do what's asked
const cantOutput = "<CANT>"

var codeFenceRegex = regexp.MustCompile("(?s)^```(?:[\\w+-]*\\n)?(.*?)\\n?```$")

// strips surrounding whitespace and any code fence enclosing the output, as models sometimes fence even plain replies
func stripFence(output string) string {
	output = strings.TrimSpace(output)
	if m := codeFenceRegex.FindStringSubmatch(output); m != nil {
		return strings.TrimSpace(m[1])
	}
	return output
}

// whether the output is <CANT>, allowing for it being quoted or fenced
func isCant(output string) bool { return normalizeOption(output) == cantOutput }

// extracts the JSON object from model output, as models sometimes wrap JSON in code fences or other text
func jsonObject(output string) string {
	start, end := strings.Index(output, "{"), strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return ""
	}
	return output[start : end+1]
}
