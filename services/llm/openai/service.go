package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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

	// the reasoning effort of models which don't reason and so reject a reasoning param
	reasoningEffortOmit shared.ReasoningEffort = ""
)

// the reasoning effort to use with each model. Reasoning adds latency and its tokens count against MaxOutputTokens,
// which can use up the budget of what are short responses, so it's turned off where possible, which also keeps logprobs
// available. Models which can't turn it off use their lowest effort, and unlisted models are assumed to be able to.
var reasoningEfforts = map[string]shared.ReasoningEffort{
	"gpt-6.1-sol":   shared.ReasoningEffortLow,
	"gpt-6-astra":   shared.ReasoningEffortLow,
	"gpt-6-sol":     shared.ReasoningEffortNone,
	"gpt-6-luna":    shared.ReasoningEffortNone,
	"gpt-5.6-sol":   shared.ReasoningEffortNone,
	"gpt-5.6-terra": shared.ReasoningEffortNone,
	"gpt-5.6-luna":  shared.ReasoningEffortNone,
	"gpt-5.5":       shared.ReasoningEffortNone,
	"gpt-5.4":       shared.ReasoningEffortNone,
	"gpt-5.4-mini":  shared.ReasoningEffortNone,
	"gpt-4.1":       reasoningEffortOmit,
	"gpt-4.1-mini":  reasoningEffortOmit,
	"gpt-4o":        reasoningEffortOmit,
	"gpt-4o-mini":   reasoningEffortOmit,
}

func reasoningEffort(model string) shared.ReasoningEffort {
	if effort, ok := reasoningEfforts[model]; ok {
		return effort
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
	// models which are reasoning can't provide logprobs
	if effort := reasoningEffort(s.model); effort != shared.ReasoningEffortNone && effort != reasoningEffortOmit {
		return ai.ClassifyByPrompt(ctx, s, input, options)
	}

	resp, logprobs, err := s.respond(ctx, ai.ClassifyInstructions(options), input, ai.ClassifyMaxTokens, true)
	if err != nil {
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
	if effort := reasoningEffort(s.model); effort != reasoningEffortOmit {
		params.Reasoning = shared.ReasoningParam{Effort: effort}
	}
	if withLogprobs {
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableMessageOutputTextLogprobs}
	}

	resp, err := s.client.Responses.New(ctx, params)
	if err != nil {
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
