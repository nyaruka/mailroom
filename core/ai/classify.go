package ai

import (
	"errors"
	"math"
	"strings"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
)

// helpers for classifying with a generative model by prompting it to reply with a category name, for LLM types
// which don't have a native classification model

// ClassifyMaxTokens is the output limit for prompt based classification, which only needs to fit a category name.
const ClassifyMaxTokens = 50

// UnscoredConfidence is the confidence given to a category chosen by a model which doesn't tell us how likely its
// choice was. The model could have declined to choose so its choice is taken as likely, but not as certain.
const UnscoredConfidence = 0.8

// ClassifyInstructions returns the instructions for prompting a generative model to classify input.
func ClassifyInstructions(categories []string) string {
	// categories rendered the same as when flows use this prompt via the prompt() function
	return prompts.Render("categorize", map[string]string{"arg1": "[" + strings.Join(categories, ", ") + "]"})
}

// NewClassification creates a classification from the response to the classify instructions. The token logprobs of
// the output are used for the confidence if the model provides them.
func NewClassification(resp *core.LLMResponse, logprobs []float64, categories []string) (*core.LLMClassification, error) {
	category := matchCategory(resp.Output, categories)
	if category == "" {
		return nil, errors.New("no category fits input")
	}

	return &core.LLMClassification{
		Category:     category,
		Confidence:   confidence(logprobs),
		TokensInput:  resp.TokensInput,
		TokensOutput: resp.TokensOutput,
	}, nil
}

// matches the output of the classify instructions against the categories, returning empty if there's no match,
// which includes the model replying <CANT>
func matchCategory(output string, categories []string) string {
	normalize := func(s string) string { return strings.Trim(strings.TrimSpace(s), `"'.`) }

	output = normalize(output)
	for _, c := range categories {
		if strings.EqualFold(output, normalize(c)) {
			return c
		}
	}
	return ""
}

// the probability of the model generating the whole output, i.e. the product of its token probabilities
func confidence(logprobs []float64) float64 {
	if len(logprobs) == 0 {
		return UnscoredConfidence
	}
	var sum float64
	for _, lp := range logprobs {
		sum += lp
	}
	return math.Exp(sum)
}
