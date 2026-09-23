package ai_test

import (
	"math"
	"testing"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/stretchr/testify/assert"
)

func TestClassifyInstructions(t *testing.T) {
	options := []*core.ClassifierOption{{Name: "Flights", Description: "Booking or changing flights"}, {Name: "Hotels"}}

	assert.Equal(t, `Classify the input text as one of the following options:

- Flights: Booking or changing flights
- Hotels

Return only the name of the most appropriate option.
Return "<CANT>" if none of the options fit.`, ai.ClassifyInstructions(options))
}

func TestNewClassification(t *testing.T) {
	options := []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}, {Name: "Car Rental"}}

	tcs := []struct {
		output     string
		logprobs   []float64
		option     string
		confidence float64
		err        string
	}{
		{output: "Hotels", option: "Hotels", confidence: ai.UnscoredConfidence},
		{output: " car rental. ", option: "Car Rental", confidence: ai.UnscoredConfidence},
		{output: `"Flights"`, option: "Flights", confidence: ai.UnscoredConfidence},
		{output: "Hotels", logprobs: []float64{0}, option: "Hotels", confidence: 1},
		{output: "Car Rental", logprobs: []float64{-0.5, -0.5}, option: "Car Rental", confidence: math.Exp(-1)},
		{output: "<CANT>", err: "no option fits input"},
		{output: "Spaceships", err: "no option fits input"},
		{output: "", err: "no option fits input"},
	}

	for _, tc := range tcs {
		cls, err := ai.NewClassification(&core.ModelResponse{Output: tc.output, TokensInput: 34, TokensOutput: 5}, tc.logprobs, options)
		if tc.err != "" {
			assert.EqualError(t, err, tc.err, "error mismatch for output %q", tc.output)
			assert.Nil(t, cls)
		} else if assert.NoError(t, err, "unexpected error for output %q", tc.output) {
			assert.Equal(t, &core.Classification{Option: tc.option, Confidence: tc.confidence, TokensInput: 34, TokensOutput: 5}, cls, "classification mismatch for output %q", tc.output)
		}
	}
}
