package openai_azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	TypeOpenAIAzure = "openai_azure"

	apiVersion = "2025-03-01-preview"

	configAPIKey   = "api_key"
	configEndpoint = "endpoint"
)

func init() {
	models.RegisterLLMService(TypeOpenAIAzure, New)
}

// an LLM service implementation for OpenAI va Microsoft Azure
type service struct {
	client openai.Client
	model  string
}

func New(rt *runtime.Runtime, m *models.LLM, c *http.Client) (flows.ModelService, error) {
	apiKey := m.Config().GetString(configAPIKey, "")
	endpoint := m.Config().GetString(configEndpoint, "")
	parsedEndpoint, err := url.Parse(endpoint)

	if apiKey == "" || endpoint == "" || err != nil {
		return nil, fmt.Errorf("config incomplete for LLM: %s", m.UUID())
	}

	// the azure middleware doesn't work with a endpoint that has a path so we strip that off
	bareEndpoint := parsedEndpoint.Scheme + "://" + parsedEndpoint.Host
	endpointPath := parsedEndpoint.Path

	// and re-add it via our own middleware
	mw := func(r *http.Request, mn option.MiddlewareNext) (*http.Response, error) {
		r.URL.Path = endpointPath + r.URL.Path
		return mn(r)
	}

	return &service{
		client: openai.NewClient(
			azure.WithEndpoint(bareEndpoint, apiVersion),
			azure.WithAPIKey(apiKey),
			option.WithMiddleware(mw),
			option.WithHTTPClient(c),
		),
		model: m.Model(),
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
	params := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(s.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(instructions),
			openai.UserMessage(input),
		},
		Temperature: openai.Float(0.000001),
		MaxTokens:   openai.Int(int64(maxTokens)),
	}
	if withLogprobs {
		params.Logprobs = openai.Bool(true)
	}

	resp, err := s.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, nil, s.error(err, instructions, input)
	}

	var logprobs []float64
	for _, lp := range resp.Choices[0].Logprobs.Content {
		logprobs = append(logprobs, lp.Logprob)
	}

	return &core.ModelResponse{
		Output:       strings.TrimSpace(resp.Choices[0].Message.Content),
		TokensInput:  resp.Usage.PromptTokens,
		TokensOutput: resp.Usage.CompletionTokens,
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
