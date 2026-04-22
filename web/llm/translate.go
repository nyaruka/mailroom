package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/llm/translate", web.JSONPayload(handleTranslate))
}

// translateConcurrency is the maximum number of LLM calls made in parallel
// for a single translate request.
const translateConcurrency = 5

// translateMaxTokens is the output token cap for a single property call. A
// single property typically holds a handful of short strings; even a maximal
// ~10KB text value translates to a few thousand output tokens, well under
// this cap and under every supported provider's model limit.
const translateMaxTokens = 8000

// Performs batch translation using an LLM. Items is a two-level map keyed by
// a caller-supplied opaque object id, then by property name.
//
//	{
//	  "org_id": 1,
//	  "llm_id": 1234,
//	  "source": "eng",
//	  "target": "spa",
//	  "items": {
//	    "a1f0e2c4-...": {
//	      "text":          ["Hi @contact.name"],
//	      "quick_replies": ["Yes", "No"]
//	    },
//	    "d4f72c66-...": {"name": ["Yes"]}
//	  }
//	}
type translateRequest struct {
	OrgID  models.OrgID                   `json:"org_id" validate:"required"`
	LLMID  models.LLMID                   `json:"llm_id" validate:"required"`
	Source i18n.Language                  `json:"source" validate:"required"`
	Target i18n.Language                  `json:"target" validate:"required"`
	Items  map[string]map[string][]string `json:"items"  validate:"required,min=1"`
}

//	{
//	  "items": {
//	    "a1f0e2c4-...": {"text": ["Hola @contact.name"]}
//	  }
//	}
type translateResponse struct {
	Items map[string]map[string][]string `json:"items"`
}

func handleTranslate(ctx context.Context, rt *runtime.Runtime, r *translateRequest) (any, int, error) {
	oa, err := models.GetOrgAssets(ctx, rt, r.OrgID)
	if err != nil {
		return nil, 0, fmt.Errorf("error loading org assets: %w", err)
	}

	llm := oa.LLMByID(r.LLMID)
	if llm == nil {
		return nil, 0, fmt.Errorf("no such LLM with ID %d", r.LLMID)
	}

	llmSvc, err := llm.AsService(http.DefaultClient)
	if err != nil {
		return nil, 0, fmt.Errorf("error creating LLM service: %w", err)
	}

	instructionsTpl := "translate"
	if r.Source == "und" || r.Source == "mul" {
		instructionsTpl = "translate_unknown_from"
	}
	instructions := prompts.Render(instructionsTpl, r)

	type result struct {
		id, prop   string
		values     []string
		tokensUsed int64
		ok         bool
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, translateConcurrency)
	results := make(chan result)

	start := time.Now()

	for id, props := range r.Items {
		for prop, vals := range props {
			wg.Add(1)
			go func(id, prop string, vals []string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				translated, tokensUsed, ok := translateValues(ctx, llmSvc, instructions, vals)
				results <- result{id: id, prop: prop, values: translated, tokensUsed: tokensUsed, ok: ok}
			}(id, prop, vals)
		}
	}

	go func() { wg.Wait(); close(results) }()

	items := make(map[string]map[string][]string)
	var totalTokens int64
	for res := range results {
		totalTokens += res.tokensUsed
		if !res.ok {
			continue
		}
		if _, ok := items[res.id]; !ok {
			items[res.id] = make(map[string][]string)
		}
		items[res.id][res.prop] = res.values
	}

	llm.RecordCall(rt, time.Since(start), totalTokens)

	return translateResponse{Items: items}, http.StatusOK, nil
}

// translateValues translates a single property's array of strings. Returns the
// translated values, the tokens used, and whether the translation succeeded.
// Any failure (LLM error, <CANT>, malformed or wrong-length JSON response) is
// reported as ok=false so the caller can simply omit the property.
func translateValues(ctx context.Context, llmSvc flows.LLMService, instructions string, vals []string) ([]string, int64, bool) {
	inputBytes, err := json.Marshal(vals)
	if err != nil {
		return nil, 0, false
	}

	resp, err := llmSvc.Response(ctx, instructions, string(inputBytes), translateMaxTokens)
	if err != nil {
		return nil, 0, false
	}

	if resp.Output == "<CANT>" {
		return nil, resp.TokensUsed, false
	}

	var translated []string
	if err := json.Unmarshal([]byte(resp.Output), &translated); err != nil {
		return nil, resp.TokensUsed, false
	}
	if len(translated) != len(vals) {
		return nil, resp.TokensUsed, false
	}
	return translated, resp.TokensUsed, true
}
