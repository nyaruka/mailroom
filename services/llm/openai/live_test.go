package openai

import (
	"context"
	"os"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/stretchr/testify/assert"
)

// TestReasoningEffortsLive checks the reasoning efforts table against the real API, and so only runs when given an API
// key. Run it when adding a model to the table.
func TestReasoningEffortsLive(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	client := openai.NewClient(option.WithAPIKey(apiKey))

	for model, effort := range reasoningEfforts {
		params := responses.ResponseNewParams{
			Model:           shared.ResponsesModel(model),
			Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String("Hello")},
			MaxOutputTokens: openai.Int(50),
		}
		if effort != reasoningEffortOmit {
			params.Reasoning = shared.ReasoningParam{Effort: effort}
		}
		if effort == shared.ReasoningEffortNone || effort == reasoningEffortOmit {
			params.Include = []responses.ResponseIncludable{responses.ResponseIncludableMessageOutputTextLogprobs}
		}

		_, err := client.Responses.New(context.Background(), params)
		assert.NoError(t, err, "model %s with effort '%s'", model, effort)
	}
}
