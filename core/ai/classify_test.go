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
		{output: "<CANT>", option: "Flights", confidence: 0},
		{output: " <CANT>. ", option: "Flights", confidence: 0},
		{output: "Spaceships", err: "model returned unknown option 'Spaceships'"},
		{output: "", err: "model returned unknown option ''"},
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
	output       string
	err          error
	instructions string
	maxTokens    int
}

func (s *promptService) Response(ctx context.Context, instructions, input string, maxTokens int) (*core.ModelResponse, error) {
	s.instructions, s.maxTokens = instructions, maxTokens
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
	options := []*core.ClassifierOption{{Name: "Flights", Description: "Booking or changing flights"}, {Name: "Hotels"}, {Name: "Car Rental"}}

	svc := &promptService{output: `{"Flights": 0.1, "Hotels": 0.85, "Car Rental": 0.05}`}
	cls, err := ai.ClassifyByPrompt(ctx, svc, "I need a room", options)
	assert.NoError(t, err)
	assert.Equal(t, &core.Classification{
		Option:        "Hotels",
		Confidence:    0.85,
		Probabilities: map[string]float64{"Flights": 0.1, "Hotels": 0.85, "Car Rental": 0.05},
		TokensInput:   34,
		TokensOutput:  2,
	}, cls)
	assert.Equal(t, `Classify the input text as one of the following options:

- Flights: Booking or changing flights
- Hotels
- Car Rental

For each option, estimate the probability between 0 and 1 that it is the correct classification of the input.
The probabilities should add up to less than 1 if the input might not fit any of the options, and all be 0 if it clearly doesn't.

Return only a JSON object with each option name as a key and its probability as the value.`, svc.instructions)
	assert.Equal(t, 110, svc.maxTokens)

	tcs := []struct {
		output     string
		option     string
		confidence float64
		probs      map[string]float64
		err        string
	}{
		{ // wrapped in a code fence, with names not exactly matching and a missing option
			output:     "```json\n{\"flights\": 0.3, \"car rental.\": 0.6}\n```",
			option:     "Car Rental",
			confidence: 0.6,
			probs:      map[string]float64{"Flights": 0.3, "Hotels": 0, "Car Rental": 0.6},
		},
		{ // scaled down if they add up to more than 1, and ties go to the first option
			output:     `{"Flights": 0.8, "Hotels": 0.8, "Car Rental": 0.4}`,
			option:     "Flights",
			confidence: 0.4,
			probs:      map[string]float64{"Flights": 0.4, "Hotels": 0.4, "Car Rental": 0.2},
		},
		{ // out of range values are clamped
			output:     `{"Flights": -0.5, "Hotels": 0.5}`,
			option:     "Hotels",
			confidence: 0.5,
			probs:      map[string]float64{"Flights": 0, "Hotels": 0.5, "Car Rental": 0},
		},
		{ // none of the options fit
			output: `{"Flights": 0, "Hotels": 0, "Car Rental": 0}`,
			option: "Flights",
			probs:  map[string]float64{"Flights": 0, "Hotels": 0, "Car Rental": 0},
		},
		{output: `{"Flights": 0.5, "Spaceships": 0.5}`, err: "model returned unknown option 'Spaceships'"},
		{output: `Hotels`, err: "model returned invalid probabilities 'Hotels'"},
		{output: `{"Hotels": "high"}`, err: `model returned invalid probabilities '{"Hotels": "high"}'`},
	}

	for _, tc := range tcs {
		cls, err := ai.ClassifyByPrompt(ctx, &promptService{output: tc.output}, "I need a room", options)
		if tc.err != "" {
			assert.EqualError(t, err, tc.err, "error mismatch for output %q", tc.output)
			assert.Nil(t, cls)
		} else if assert.NoError(t, err, "unexpected error for output %q", tc.output) {
			assert.Equal(t, tc.option, cls.Option, "option mismatch for output %q", tc.output)
			assert.InDelta(t, tc.confidence, cls.Confidence, 0.0001, "confidence mismatch for output %q", tc.output)
			assert.InDeltaMapValues(t, tc.probs, cls.Probabilities, 0.0001, "probabilities mismatch for output %q", tc.output)
		}
	}

	cls, err = ai.ClassifyByPrompt(ctx, &promptService{err: errors.New("boom")}, "I need a room", options)
	assert.EqualError(t, err, "boom")
	assert.Nil(t, cls)
}
