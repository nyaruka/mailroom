package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
)

// helpers for classifying with a generative model by prompting it, for model types which don't have a native
// classification model. Like a native classifier, they always choose one of the options and leave it to flows to decide
// whether the confidence is high enough, so a model that judges that none of the options fit gives no confidence in
// the first option rather than an error.

// ClassifyMaxTokens is the output limit for the classify instructions, which only needs to fit an option name.
const ClassifyMaxTokens = 50

// classifyScoredMaxTokens is the output limit for the scored classify instructions, before allowing for the options.
// It leaves room for a model that writes a sentence or two before the JSON despite being told not to, which would
// otherwise be cut off before the JSON is complete.
const classifyScoredMaxTokens = 200

// UnscoredConfidence is the confidence given to an option chosen by a model which was expected to provide logprobs
// but didn't. The model could have declined to choose so its choice is taken as likely, but not as certain.
const UnscoredConfidence = core.ClassifierConfidenceMedium

// minimum probabilities for each confidence level, in descending order
var confidenceThresholds = []struct {
	level core.ClassifierConfidence
	min   float64
}{
	{core.ClassifierConfidenceHigh, 0.9},
	{core.ClassifierConfidenceMedium, 0.7},
	{core.ClassifierConfidenceLow, 0.4},
}

// ConfidenceLevel maps the probability of a classification onto a confidence level
func ConfidenceLevel(p float64) core.ClassifierConfidence {
	for _, t := range confidenceThresholds {
		if p >= t.min {
			return t.level
		}
	}
	return core.ClassifierConfidenceNone
}

// ClassifyInstructions returns the instructions for prompting a generative model to reply with the name of an option,
// for services whose API provides the logprobs of the reply.
func ClassifyInstructions(options []*core.ClassifierOption) string {
	return prompts.Render("classify", map[string]any{"Options": options})
}

// NewClassification creates a classification from the response to the classify instructions. The token logprobs of
// the output are used for the confidence. Options can't be empty, which flows ensure.
func NewClassification(resp *core.ModelResponse, logprobs []float64, options []*core.ClassifierOption) (*core.Classification, error) {
	cls := &core.Classification{Option: options[0].Name, Confidence: core.ClassifierConfidenceNone, Tokens: resp.Tokens}

	// the model can't tell us which option comes closest so we can only give no confidence in any of them
	if isCant(resp.Output) {
		return cls, nil
	}

	option := matchOption(resp.Output, options)
	if option == "" {
		return nil, fmt.Errorf("model returned unknown option '%s'", resp.Output)
	}

	cls.Option = option
	cls.Confidence = confidence(logprobs)
	return cls, nil
}

// ClassifyByPrompt classifies input by prompting the given service to state the probability of each option, for
// services whose API doesn't provide logprobs. Stated probabilities aren't well calibrated but do preserve the order of
// the model's preferences. Options can't be empty, which flows ensure.
func ClassifyByPrompt(ctx context.Context, svc flows.ModelService, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	instructions := prompts.Render("classify_scored", map[string]any{"Options": options})

	// allow for a preamble, and each option name, its probability and the JSON punctuation
	resp, err := svc.Response(ctx, instructions, input, classifyScoredMaxTokens+20*len(options))
	if err != nil {
		return nil, err
	}

	probs, err := parseProbabilities(resp.Output, options)
	if err != nil {
		return nil, err
	}

	option, best := options[0].Name, 0.0
	for _, o := range options {
		if probs[o.Name] > best {
			option, best = o.Name, probs[o.Name]
		}
	}
	return &core.Classification{Option: option, Confidence: ConfidenceLevel(best), Probabilities: probs, Tokens: resp.Tokens}, nil
}

// parses the JSON object of option probabilities from the output of the scored classify instructions, filling in any
// missing options as zero and scaling them down if they add up to more than 1
func parseProbabilities(output string, options []*core.ClassifierOption) (map[string]float64, error) {
	var stated map[string]float64
	if err := json.Unmarshal([]byte(jsonObject(output)), &stated); err != nil {
		return nil, fmt.Errorf("model returned invalid probabilities '%s'", output)
	}

	probs := make(map[string]float64, len(options))
	for _, o := range options {
		probs[o.Name] = 0
	}

	for name, p := range stated {
		option := matchOption(name, options)
		if option == "" {
			return nil, fmt.Errorf("model returned unknown option '%s'", name)
		}
		probs[option] = max(probs[option], min(1, p))
	}

	var total float64
	for _, p := range probs {
		total += p
	}
	if total > 1 {
		for name := range probs {
			probs[name] /= total
		}
	}

	return probs, nil
}

func normalizeOption(s string) string { return strings.Trim(StripFence(s), `"'.`) }

// matches a model's output against the option names, returning empty if there's no match
func matchOption(output string, options []*core.ClassifierOption) string {
	output = normalizeOption(output)
	for _, o := range options {
		if strings.EqualFold(output, normalizeOption(o.Name)) {
			return o.Name
		}
	}
	return ""
}

// the confidence level of the probability of the model generating the whole output, i.e. the product of its token
// probabilities
func confidence(logprobs []float64) core.ClassifierConfidence {
	if len(logprobs) == 0 {
		return UnscoredConfidence
	}
	var sum float64
	for _, lp := range logprobs {
		sum += lp
	}
	return ConfidenceLevel(math.Exp(sum))
}
