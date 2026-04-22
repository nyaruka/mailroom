package models

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test/services"
)

// testLLMService wraps the goflow test LLM service and additionally handles
// batch-translate JSON inputs from the web/llm endpoint. For everything else
// it delegates to the wrapped service.
type testLLMService struct {
	inner *services.LLMService
}

func newTestLLMService() *testLLMService {
	return &testLLMService{inner: services.NewLLM()}
}

func (s *testLLMService) Response(ctx context.Context, instructions, input string, maxTokens int) (*flows.LLMResponse, error) {
	if strings.HasPrefix(instructions, "Translate each value in the JSON input") {
		return s.translateBatch(instructions, input)
	}
	return s.inner.Response(ctx, instructions, input, maxTokens)
}

// translateBatch produces a deterministic JSON response for batch translation
// prompts. Each value is prefixed with "T-" unless it's a test directive:
//
//   - "<SKIP>"  → the containing property is omitted from the response
//   - "<BAD>"   → the whole response is returned as non-JSON
func (s *testLLMService) translateBatch(instructions, input string) (*flows.LLMResponse, error) {
	var items map[string]map[string][]string
	if err := json.Unmarshal([]byte(input), &items); err != nil {
		return &flows.LLMResponse{Output: "not valid json", TokensUsed: 123}, nil
	}

	out := make(map[string]map[string][]string, len(items))
	for id, props := range items {
		outProps := make(map[string][]string)
		for prop, vals := range props {
			skip := false
			translated := make([]string, len(vals))
			for i, v := range vals {
				switch v {
				case "<SKIP>":
					skip = true
				case "<BAD>":
					return &flows.LLMResponse{Output: "not valid json", TokensUsed: 123}, nil
				default:
					translated[i] = "T-" + v
				}
			}
			if !skip {
				outProps[prop] = translated
			}
		}
		if len(outProps) > 0 {
			out[id] = outProps
		}
	}

	outBytes, _ := json.Marshal(out)
	return &flows.LLMResponse{Output: string(outBytes), TokensUsed: 123}, nil
}

var _ flows.LLMService = (*testLLMService)(nil)
