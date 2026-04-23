package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/engine"
	"github.com/nyaruka/goflow/test/services"
	"github.com/nyaruka/mailroom/v26/core/goflow"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/null/v3"
)

// LLMID is our type for LLM IDs
type LLMID int

// NilLLMID is nil value for LLM IDs
const NilLLMID = LLMID(0)

var registeredLLMServices = map[string]func(*LLM, *http.Client) (flows.LLMService, error){}

// Register a LLM service factory with the engine
func init() {
	RegisterLLMService("test", func(*LLM, *http.Client) (flows.LLMService, error) {
		return &testLLMService{inner: services.NewLLM()}, nil
	})

	goflow.RegisterLLMServiceFactory(llmServiceFactory)
}

// testLLMService wraps goflow's test LLM service so that directives like
// "\return ..." and "\error ..." also work when the input is a JSON object of
// string arrays (as used by the translate endpoint). It finds the first string
// value in sorted-key order that starts with a directive and forwards that to
// the underlying service as the input.
type testLLMService struct {
	inner flows.LLMService
}

func (s *testLLMService) Response(ctx context.Context, instructions, input string, maxTokens int) (*flows.LLMResponse, error) {
	if len(input) > 0 && input[0] == '{' {
		var obj map[string][]string
		if err := json.Unmarshal([]byte(input), &obj); err == nil {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if len(obj[k]) > 0 && (strings.HasPrefix(obj[k][0], "\\return ") || strings.HasPrefix(obj[k][0], "\\error ")) {
					return s.inner.Response(ctx, instructions, obj[k][0], maxTokens)
				}
			}
		}
	}
	return s.inner.Response(ctx, instructions, input, maxTokens)
}

// RegisterLLMService registers a LLM service for the given type code
func RegisterLLMService(typ string, fn func(*LLM, *http.Client) (flows.LLMService, error)) {
	registeredLLMServices[typ] = fn
}

func llmServiceFactory(rt *runtime.Runtime) engine.LLMServiceFactory {
	httpClient, _ := goflow.HTTP(rt.Config)

	return func(llm *flows.LLM) (flows.LLMService, error) {
		return llm.Asset().(*LLM).AsService(httpClient)
	}
}

// LLM is our type for a large language model
type LLM struct {
	ID_              LLMID          `json:"id"`
	UUID_            assets.LLMUUID `json:"uuid"`
	Type_            string         `json:"llm_type"`
	Model_           string         `json:"model"`
	Name_            string         `json:"name"`
	Config_          Config         `json:"config"`
	MaxOutputTokens_ int            `json:"max_output_tokens"`
}

func (l *LLM) ID() LLMID            { return l.ID_ }
func (l *LLM) UUID() assets.LLMUUID { return l.UUID_ }
func (l *LLM) Name() string         { return l.Name_ }
func (l *LLM) Type() string         { return l.Type_ }
func (l *LLM) Model() string        { return l.Model_ }
func (l *LLM) Config() Config       { return l.Config_ }
func (l *LLM) MaxOutputTokens() int { return l.MaxOutputTokens_ }

func (l *LLM) AsService(client *http.Client) (flows.LLMService, error) {
	fn := registeredLLMServices[l.Type()]
	if fn == nil {
		return nil, fmt.Errorf("unknown type '%s' for LLM: %s", l.Type(), l.UUID())
	}
	return fn(l, client)
}

func (l *LLM) RecordCall(rt *runtime.Runtime, d time.Duration, tokensUsed int64) {
	// TODO record tokens used ?

	rt.Stats.RecordLLMCall(l.Type(), l.Model(), d)
}

// loads the LLMs for the passed in org
func loadLLMs(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.LLM, error) {
	rows, err := db.QueryContext(ctx, sqlSelectLLMs, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying LLMs for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.LLM { return &LLM{} })
}

const sqlSelectLLMs = `
SELECT ROW_TO_JSON(r) FROM (
      SELECT l.id, l.uuid, l.name, l.llm_type, l.model, l.config, l.max_output_tokens
        FROM ai_llm l
       WHERE l.org_id = $1 AND l.is_active
    ORDER BY l.created_on ASC
) r;`

func (i *LLMID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i LLMID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *LLMID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i LLMID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
