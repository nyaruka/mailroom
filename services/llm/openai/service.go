package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	TypeOpenAI = "openai"

	configAPIKey = "api_key"

	// the reasoning effort of models which don't take a reasoning param, or reject all the efforts we try
	reasoningEffortOmit shared.ReasoningEffort = ""
)

var errLogprobsUnsupported = errors.New("logprobs not supported by model")

// the reasoning effort to use with each model, learned from the API rejecting what we tried. Reasoning adds latency
// and its tokens count against MaxOutputTokens, which can use up the budget of what are short responses, so we start
// by trying to turn it off. Models which can't turn it off use the lowest effort that reasoning models generally
// accept, and models which don't reason or reject that too get no reasoning param at all.
var reasoningEfforts sync.Map

// models which have rejected requests for logprobs, which happens when they're reasoning
var logprobsUnsupported sync.Map

func reasoningEffort(model string) shared.ReasoningEffort {
	if effort, ok := reasoningEfforts.Load(model); ok {
		return effort.(shared.ReasoningEffort)
	}
	return shared.ReasoningEffortNone
}

func init() {
	models.RegisterLLMService(TypeOpenAI, New)
}

// an LLM service implementation for OpenAI
type service struct {
	client          openai.Client
	model           string
	maxOutputTokens int
}

func New(rt *runtime.Runtime, m *models.LLM, c *http.Client) (flows.ModelService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	return &service{
		client:          openai.NewClient(option.WithAPIKey(apiKey), option.WithHTTPClient(c)),
		model:           m.Model(),
		maxOutputTokens: m.MaxOutputTokens(),
	}, nil
}

func (s *service) Response(ctx context.Context, instructions, input string, maxTokens int) (*core.ModelResponse, error) {
	resp, _, err := s.respond(ctx, instructions, input, maxTokens, false)
	return resp, err
}

func (s *service) Classify(ctx context.Context, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	if _, unsupported := logprobsUnsupported.Load(s.model); unsupported {
		return ai.ClassifyByPrompt(ctx, s, input, options)
	}

	resp, logprobs, err := s.respond(ctx, ai.ClassifyInstructions(options), input, ai.ClassifyMaxTokens, true)
	if errors.Is(err, errLogprobsUnsupported) {
		logprobsUnsupported.Store(s.model, true)
		return ai.ClassifyByPrompt(ctx, s, input, options)
	} else if err != nil {
		return nil, err
	}

	return ai.NewClassification(resp, logprobs, options)
}

func (s *service) Translate(ctx context.Context, source, target i18n.Language, items map[string][]string) (*core.Translation, error) {
	return ai.TranslateByPrompt(ctx, s, source, target, items, s.maxOutputTokens)
}

// generates a response, optionally with the logprobs of its output tokens
func (s *service) respond(ctx context.Context, instructions, input string, maxTokens int, withLogprobs bool) (*core.ModelResponse, []float64, error) {
	params := responses.ResponseNewParams{
		Model:        shared.ResponsesModel(s.model),
		Instructions: openai.String(instructions),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(input),
		},
		MaxOutputTokens: openai.Int(int64(maxTokens)),
	}
	if withLogprobs {
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableMessageOutputTextLogprobs}
	}

	resp, err := s.newResponse(ctx, params)
	if withLogprobs && isUnsupported(err, "include") {
		return nil, nil, errLogprobsUnsupported
	} else if err != nil {
		return nil, nil, s.error(err, instructions, input)
	}

	output := strings.TrimSpace(resp.OutputText())
	if output == "" && resp.Status == responses.ResponseStatusIncomplete {
		msg := fmt.Sprintf("response incomplete with no output: %s", resp.IncompleteDetails.Reason)
		return nil, nil, &ai.ServiceError{Message: msg, Code: ai.ErrorUnknown, Instructions: instructions, Input: input}
	}

	var logprobs []float64
	for _, item := range resp.Output {
		for _, content := range item.Content {
			for _, lp := range content.Logprobs {
				logprobs = append(logprobs, lp.Logprob)
			}
		}
	}

	return &core.ModelResponse{
		Output: output,
		Tokens: core.ModelTokens{Input: resp.Usage.InputTokens, Output: resp.Usage.OutputTokens},
	}, logprobs, nil
}

// creates a response with the model's reasoning effort, retrying with the next effort if the API rejects it
func (s *service) newResponse(ctx context.Context, params responses.ResponseNewParams) (*responses.Response, error) {
	effort := reasoningEffort(s.model)
	for {
		params.Reasoning = shared.ReasoningParam{}
		if effort != reasoningEffortOmit {
			params.Reasoning = shared.ReasoningParam{Effort: effort}
		}

		resp, err := s.client.Responses.New(ctx, params)
		if !isUnsupported(err, "reasoning.effort") || effort == reasoningEffortOmit {
			return resp, err
		}

		if aerr, _ := errors.AsType[*responses.Error](err); aerr.Code == "unsupported_value" && effort == shared.ReasoningEffortNone {
			effort = shared.ReasoningEffortLow
		} else {
			effort = reasoningEffortOmit
		}
		reasoningEfforts.Store(s.model, effort)
	}
}

// whether the given error is the API rejecting the given request param as unsupported by the model
func isUnsupported(err error, param string) bool {
	aerr, ok := errors.AsType[*responses.Error](err)
	return ok && aerr.StatusCode == http.StatusBadRequest && aerr.Param == param &&
		(aerr.Code == "unsupported_parameter" || aerr.Code == "unsupported_value")
}

func (s *service) error(err error, instructions, input string) error {
	code := ai.ErrorUnknown
	if aerr, ok := errors.AsType[*responses.Error](err); ok {
		switch aerr.StatusCode {
		case http.StatusUnauthorized:
			code = ai.ErrorCredentials
		case http.StatusTooManyRequests:
			code = ai.ErrorRateLimit
		}
	}
	return &ai.ServiceError{Message: err.Error(), Code: code, Instructions: instructions, Input: input}
}
