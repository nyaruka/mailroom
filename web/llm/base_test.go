package llm_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/nyaruka/mailroom/v26/web/llm"
)

// an LLM service which never responds, only returning once its context is done
type slowLLMService struct{}

func (s *slowLLMService) Response(ctx context.Context, instructions, input string, maxTokens int) (*core.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTranslate(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// LLM without the editing role - id will be 30000
	testdb.InsertLLM(t, rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "test", "gpt-4", "Engine Only", map[string]any{}, "F")

	// LLM which is too slow to respond - id will be 30001
	models.RegisterLLMService("slow", func(*runtime.Runtime, *models.LLM, *http.Client) (flows.LLMService, error) {
		return &slowLLMService{}, nil
	})
	testdb.InsertLLM(t, rt, testdb.Org1, "0e4d2ef0-6a4c-4f6a-a5a2-1f3a0f0a3c5e", "slow", "sloth-1", "Slow", map[string]any{}, "TF")

	defer func(d time.Duration) { llm.CallTimeout = d }(llm.CallTimeout)
	llm.CallTimeout = 100 * time.Millisecond

	testsuite.RunWebTests(t, rt, "testdata/translate.json")
}
