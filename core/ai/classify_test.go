package ai_test

import (
	"math"
	"testing"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/stretchr/testify/assert"
)

func TestClassifyInstructions(t *testing.T) {
	assert.Equal(t, "Categorize the input text into one of the following categories: [Flights, Hotels].\nReturn only the name of the most appropriate category.\nReturn \"<CANT>\" if you can't categorize it.", ai.ClassifyInstructions([]string{"Flights", "Hotels"}))
}

func TestNewClassification(t *testing.T) {
	categories := []string{"Flights", "Hotels", "Car Rental"}

	tcs := []struct {
		output     string
		logprobs   []float64
		category   string
		confidence float64
		err        string
	}{
		{output: "Hotels", category: "Hotels", confidence: ai.UnscoredConfidence},
		{output: " car rental. ", category: "Car Rental", confidence: ai.UnscoredConfidence},
		{output: `"Flights"`, category: "Flights", confidence: ai.UnscoredConfidence},
		{output: "Hotels", logprobs: []float64{0}, category: "Hotels", confidence: 1},
		{output: "Car Rental", logprobs: []float64{-0.5, -0.5}, category: "Car Rental", confidence: math.Exp(-1)},
		{output: "<CANT>", err: "no category fits input"},
		{output: "Spaceships", err: "no category fits input"},
		{output: "", err: "no category fits input"},
	}

	for _, tc := range tcs {
		cls, err := ai.NewClassification(&core.LLMResponse{Output: tc.output, TokensInput: 34, TokensOutput: 5}, tc.logprobs, categories)
		if tc.err != "" {
			assert.EqualError(t, err, tc.err, "error mismatch for output %q", tc.output)
			assert.Nil(t, cls)
		} else if assert.NoError(t, err, "unexpected error for output %q", tc.output) {
			assert.Equal(t, &core.LLMClassification{Category: tc.category, Confidence: tc.confidence, TokensInput: 34, TokensOutput: 5}, cls, "classification mismatch for output %q", tc.output)
		}
	}
}
