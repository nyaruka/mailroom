package models

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test/services"
)

// testLLMService wraps the goflow test LLM service and additionally handles
// per-property translate calls from the web/llm endpoint. The translate
// endpoint sends a JSON array of strings; we return a JSON array of the same
// strings each prefixed with "T-". For everything else we delegate.
type testLLMService struct {
	inner *services.LLMService
}

func newTestLLMService() *testLLMService {
	return &testLLMService{inner: services.NewLLM()}
}

func (s *testLLMService) Response(ctx context.Context, instructions, input string, maxTokens int) (*flows.LLMResponse, error) {
	if strings.HasPrefix(instructions, "Translate each string in the JSON array input") {
		return s.translateValues(input)
	}
	return s.inner.Response(ctx, instructions, input, maxTokens)
}

// translateValues produces a deterministic response for a per-property
// translate call. Each string is prefixed with "T-", with these in-band
// directives:
//
//   - "<CANT>"  → return the literal "<CANT>" sentinel
//   - "<BAD>"   → return non-JSON, simulating a malformed LLM response
func (s *testLLMService) translateValues(input string) (*flows.LLMResponse, error) {
	var values []string
	if err := json.Unmarshal([]byte(input), &values); err != nil {
		return &flows.LLMResponse{Output: "not valid json", TokensUsed: 123}, nil
	}

	for _, v := range values {
		switch v {
		case "<CANT>":
			return &flows.LLMResponse{Output: "<CANT>", TokensUsed: 123}, nil
		case "<BAD>":
			return &flows.LLMResponse{Output: "not valid json", TokensUsed: 123}, nil
		}
	}

	translated := make([]string, len(values))
	for i, v := range values {
		translated[i] = "T-" + v
	}

	outBytes, _ := json.Marshal(translated)
	return &flows.LLMResponse{Output: string(outBytes), TokensUsed: 123}, nil
}

var _ flows.LLMService = (*testLLMService)(nil)
