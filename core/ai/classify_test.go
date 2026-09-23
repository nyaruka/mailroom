package ai_test

import (
	"context"
	"errors"
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

type promptService struct {
	output string
	err    error
}

func (s *promptService) Response(ctx context.Context, instructions, input string, maxTokens int) (*core.ModelResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &core.ModelResponse{Output: s.output, TokensInput: 34, TokensOutput: 2}, nil
}

func (s *promptService) Classify(ctx context.Context, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	return ai.ClassifyByPrompt(ctx, s, input, options)
}

func TestClassifyByPrompt(t *testing.T) {
	ctx := context.Background()
	options := []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}}

	cls, err := ai.ClassifyByPrompt(ctx, &promptService{output: "Hotels"}, "I need a room", options)
	assert.NoError(t, err)
	assert.Equal(t, &core.Classification{Option: "Hotels", Confidence: ai.UnscoredConfidence, TokensInput: 34, TokensOutput: 2}, cls)

	cls, err = ai.ClassifyByPrompt(ctx, &promptService{output: "<CANT>"}, "What's the weather?", options)
	assert.EqualError(t, err, "no option fits input")
	assert.Nil(t, cls)

	cls, err = ai.ClassifyByPrompt(ctx, &promptService{err: errors.New("boom")}, "I need a room", options)
	assert.EqualError(t, err, "boom")
	assert.Nil(t, cls)
}
