package ai

import (
	"errors"
	"math"
	"strings"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
)

// helpers for classifying with a generative model by prompting it to reply with an option name, for model types
// which don't have a native classification model

// ClassifyMaxTokens is the output limit for prompt based classification, which only needs to fit an option name.
const ClassifyMaxTokens = 50

// UnscoredConfidence is the confidence given to an option chosen by a model which doesn't tell us how likely its
// choice was. The model could have declined to choose so its choice is taken as likely, but not as certain.
const UnscoredConfidence = 0.8

// ClassifyInstructions returns the instructions for prompting a generative model to classify input.
func ClassifyInstructions(options []*core.ClassifierOption) string {
	return prompts.Render("classify", map[string]any{"Options": options})
}

// NewClassification creates a classification from the response to the classify instructions. The token logprobs of
// the output are used for the confidence if the model provides them.
func NewClassification(resp *core.ModelResponse, logprobs []float64, options []*core.ClassifierOption) (*core.Classification, error) {
	option := matchOption(resp.Output, options)
	if option == "" {
		return nil, errors.New("no option fits input")
	}

	return &core.Classification{
		Option:       option,
		Confidence:   confidence(logprobs),
		TokensInput:  resp.TokensInput,
		TokensOutput: resp.TokensOutput,
	}, nil
}

// matches the output of the classify instructions against the option names, returning empty if there's no match,
// which includes the model replying <CANT>
func matchOption(output string, options []*core.ClassifierOption) string {
	normalize := func(s string) string { return strings.Trim(strings.TrimSpace(s), `"'.`) }

	output = normalize(output)
	for _, o := range options {
		if strings.EqualFold(output, normalize(o.Name)) {
			return o.Name
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
