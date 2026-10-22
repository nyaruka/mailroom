package openai

import (
	"context"
	"fmt"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/openai/openai-go/v3"
)

// models which can be used with the Decisions API, which classifies natively rather than by prompting a model
var decisionModels = map[string]bool{
	"gpt-6-luna": true,
}

const decideQuestionName = "option"

// the minimum probability of a predicate for it to be a yes. Below this it's a no, which is no confidence in the option
// even if the probability would otherwise reach a confidence level.
const predicateYesMin = 0.5

// newDecideParams builds a request to classify the input. A choice question needs at least two choices, so a single
// option is asked as a predicate instead.
func newDecideParams(model, input string, options []*core.ClassifierOption) openai.DecisionNewParams {
	var q openai.DecisionNewParamsQuestionUnion
	if len(options) == 1 {
		o := options[0]
		instructions := fmt.Sprintf("This input matches %q.", o.Name)
		if o.Description != "" {
			instructions = fmt.Sprintf("This input matches %q (%s).", o.Name, o.Description)
		}
		q.OfPredicate = &openai.DecisionNewParamsQuestionPredicate{Instructions: instructions, Name: openai.String(decideQuestionName)}
	} else {
		choices := make([]openai.DecisionNewParamsQuestionChoiceChoice, len(options))
		for i, o := range options {
			choices[i] = openai.DecisionNewParamsQuestionChoiceChoice{
				Value: openai.DecisionNewParamsQuestionChoiceChoiceValueUnion{OfString: openai.String(o.Name)},
			}
			if o.Description != "" {
				choices[i].Description = openai.String(o.Description)
			}
		}
		q.OfChoice = &openai.DecisionNewParamsQuestionChoice{
			Instructions: "Which option best fits this input?",
			Choices:      choices,
			Name:         openai.String(decideQuestionName),
		}
	}

	return openai.DecisionNewParams{
		Model:     model,
		Input:     openai.DecisionNewParamsInputUnion{OfString: openai.String(input)},
		Questions: []openai.DecisionNewParamsQuestionUnion{q},
	}
}

func (s *service) classifyByDecision(ctx context.Context, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	params := newDecideParams(s.model, input, options)
	instructions := *params.Questions[0].GetInstructions()

	resp, err := s.client.Decisions.New(ctx, params)
	if err != nil {
		return nil, s.error(err, instructions, input)
	}

	cls, err := toClassification(resp, options)
	if err != nil {
		return nil, &ai.ServiceError{Message: err.Error(), Code: ai.ErrorUnknown, Instructions: instructions, Input: input}
	}
	return cls, nil
}

// toClassification maps the response to a request from newDecideParams onto a classification
func toClassification(resp *openai.Decision, options []*core.ClassifierOption) (*core.Classification, error) {
	if len(resp.Answers) != 1 {
		return nil, fmt.Errorf("response has %d answers, expected 1", len(resp.Answers))
	}

	cls := &core.Classification{
		Option:     options[0].Name,
		Confidence: core.ClassifierConfidenceNone,
		Tokens:     core.ModelTokens{Input: resp.Usage.InputTokens, Output: resp.Usage.OutputTokens},
	}

	switch ans := resp.Answers[0].AsAny().(type) {
	case openai.DecisionAnswerRefusal:
		// like a model that can't choose, a refusal is no confidence in any option
	case openai.DecisionAnswerPredicate:
		// a no is no confidence in the only option, so that flows route it to none
		cls.Probabilities = map[string]float64{options[0].Name: ans.Probability}
		if ans.Probability >= predicateYesMin {
			cls.Confidence = ai.ConfidenceLevel(ans.Probability)
		}
	case openai.DecisionAnswerChoice:
		choice := ans.Choice.AsString()
		known := false
		for _, o := range options {
			known = known || o.Name == choice
		}
		if !known {
			return nil, fmt.Errorf("response has unknown choice: %s", choice)
		}

		cls.Option = choice
		cls.Confidence = ai.ConfidenceLevel(ans.Confidence)
		// keyed by exactly the option names, like the other classify paths
		cls.Probabilities = make(map[string]float64, len(options))
		for _, o := range options {
			cls.Probabilities[o.Name] = 0
		}
		for _, p := range ans.Probabilities {
			if _, ok := cls.Probabilities[p.Value.AsString()]; ok {
				cls.Probabilities[p.Value.AsString()] = p.Probability
			}
		}
	default:
		return nil, fmt.Errorf("response has unexpected answer type: %s", resp.Answers[0].Type)
	}

	return cls, nil
}
