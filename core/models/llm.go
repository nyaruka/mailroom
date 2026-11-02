package models

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/tallies"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
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

// maxOutputTokensLimit is the hard ceiling we apply to any LLM's configured max_output_tokens.
const maxOutputTokensLimit = 16000

var registeredLLMServices = map[string]func(*runtime.Runtime, *LLM, *http.Client) (flows.ModelService, error){}

// Register a LLM service factory with the engine
func init() {
	RegisterLLMService("test", func(*runtime.Runtime, *LLM, *http.Client) (flows.ModelService, error) {
		return services.NewModel(), nil
	})

	goflow.RegisterModelServiceFactory(modelServiceFactory)
}

// RegisterLLMService registers a LLM service for the given type code
func RegisterLLMService(typ string, fn func(*runtime.Runtime, *LLM, *http.Client) (flows.ModelService, error)) {
	registeredLLMServices[typ] = fn
}

func modelServiceFactory(rt *runtime.Runtime) engine.ModelServiceFactory {
	return func(llm *core.Model) (flows.ModelService, error) {
		return llm.Asset().(*LLM).AsService(rt)
	}
}

// LLM is our type for a large language model
type LLM struct {
	ID_              LLMID              `json:"id"`
	UUID_            assets.ModelUUID   `json:"uuid"`
	OrgID_           OrgID              `json:"org_id"`
	Type_            string             `json:"llm_type"`
	Model_           string             `json:"model"`
	Name_            string             `json:"name"`
	Config_          Config             `json:"config"`
	MaxOutputTokens_ int                `json:"max_output_tokens"`
	Roles_           []assets.ModelRole `json:"roles"`
}

func (l *LLM) ID() LLMID                 { return l.ID_ }
func (l *LLM) OrgID() OrgID              { return l.OrgID_ }
func (l *LLM) UUID() assets.ModelUUID    { return l.UUID_ }
func (l *LLM) Name() string              { return l.Name_ }
func (l *LLM) Type() string              { return l.Type_ }
func (l *LLM) Model() string             { return l.Model_ }
func (l *LLM) Config() Config            { return l.Config_ }
func (l *LLM) MaxOutputTokens() int      { return min(l.MaxOutputTokens_, maxOutputTokensLimit) }
func (l *LLM) Roles() []assets.ModelRole { return l.Roles_ }

// AsService constructs the service for this LLM. It's always given rt.HTTP.Services - the client for fixed
// outbound targets - rather than letting the caller choose, because a caller which passes something else
// (e.g. http.DefaultClient) silently loses that client's timeout, connection pooling and, since tracing
// became a property of the client rather than of each call, its trace capture too.
func (l *LLM) AsService(rt *runtime.Runtime) (flows.ModelService, error) {
	fn := registeredLLMServices[l.Type()]
	if fn == nil {
		return nil, fmt.Errorf("unknown type '%s' for LLM: %s", l.Type(), l.UUID())
	}
	return fn(rt, l, rt.HTTP.Services)
}

// RecordCall records stats for an LLM call and returns the amounts to add to the org's tallies.
func (l *LLM) RecordCall(rt *runtime.Runtime, elapsed time.Duration, tokens core.ModelTokens) map[string]int64 {
	rt.Stats.RecordLLMCall(l.Type(), l.Model(), elapsed)

	now := dates.Now()
	scope := func(name string) string { return tallies.Daily(fmt.Sprintf("%s/%d/%s", TallyLLMs, l.ID(), name), now) }

	amounts := map[string]int64{scope("calls"): 1}
	if tokens.Input > 0 {
		amounts[scope("tokens:in")] = tokens.Input
	}
	if tokens.Output > 0 {
		amounts[scope("tokens:out")] = tokens.Output
	}
	return amounts
}

// loads the LLMs for the passed in org. Roles are stored as codes: T (translate), G (generate), C (classify)
func loadLLMs(ctx context.Context, db *sql.DB, orgID OrgID) ([]assets.Model, error) {
	rows, err := db.QueryContext(ctx, sqlSelectLLMs, orgID)
	if err != nil {
		return nil, fmt.Errorf("error querying LLMs for org: %d: %w", orgID, err)
	}

	return ScanJSONRows(rows, func() assets.Model { return &LLM{} })
}

const sqlSelectLLMs = `
SELECT ROW_TO_JSON(r) FROM (
      SELECT l.id, l.uuid, l.org_id, l.llm_type, l.model, l.name, l.config, l.max_output_tokens,
             ARRAY_REMOVE(ARRAY[
                 CASE WHEN l.roles ~ 'T' THEN 'translate' END,
                 CASE WHEN l.roles ~ 'G' THEN 'generate' END,
                 CASE WHEN l.roles ~ 'C' THEN 'classify' END
             ], NULL) AS roles
        FROM ai_llm l
       WHERE l.org_id = $1 AND l.is_active
    ORDER BY l.created_on ASC
) r;`

func (i *LLMID) Scan(value any) error         { return null.ScanInt(value, i) }
func (i LLMID) Value() (driver.Value, error)  { return null.IntValue(i) }
func (i *LLMID) UnmarshalJSON(b []byte) error { return null.UnmarshalInt(b, i) }
func (i LLMID) MarshalJSON() ([]byte, error)  { return null.MarshalInt(i) }
