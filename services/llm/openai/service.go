package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
)

func init() {
	models.RegisterLLMService(TypeOpenAI, New)
}

// an LLM service implementation for OpenAI
type service struct {
	client openai.Client
	model  string
}

func New(rt *runtime.Runtime, m *models.LLM, c *http.Client) (flows.ModelService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	if apiKey == "" {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	return &service{
		client: openai.NewClient(option.WithAPIKey(apiKey), option.WithHTTPClient(c)),
		model:  m.Model(),
	}, nil
}

func (s *service) Response(ctx context.Context, instructions, input string, maxTokens int) (*core.ModelResponse, error) {
	resp, _, err := s.respond(ctx, instructions, input, maxTokens, false)
	return resp, err
}

func (s *service) Classify(ctx context.Context, input string, options []*core.ClassifierOption) (*core.Classification, error) {
	resp, logprobs, err := s.respond(ctx, ai.ClassifyInstructions(options), input, ai.ClassifyMaxTokens, true)
	if err != nil {
		return nil, err
	}

	return ai.NewClassification(resp, logprobs, options)
}

// generates a response, optionally with the logprobs of its output tokens
func (s *service) respond(ctx context.Context, instructions, input string, maxTokens int, withLogprobs bool) (*core.ModelResponse, []float64, error) {
	params := responses.ResponseNewParams{
		Model:        shared.ResponsesModel(s.model),
		Instructions: openai.String(instructions),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(input),
		},
		Temperature:     openai.Float(0.000001),
		MaxOutputTokens: openai.Int(int64(maxTokens)),
	}
	if withLogprobs {
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableMessageOutputTextLogprobs}
	}

	resp, err := s.client.Responses.New(ctx, params)
	if err != nil {
		return nil, nil, s.error(err, instructions, input)
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
		Output:       strings.TrimSpace(resp.OutputText()),
		TokensInput:  resp.Usage.InputTokens,
		TokensOutput: resp.Usage.OutputTokens,
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
