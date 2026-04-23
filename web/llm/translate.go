package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/mailroom/v26/core/ai"
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

// translateMaxTokens is the output token cap for a single item call. An item
// typically holds a handful of short strings; even a maximal ~10KB text value
// translates to a few thousand output tokens, well under this cap and under
// every supported provider's model limit.
const translateMaxTokens = 8000

// Performs batch translation using an LLM. Items is a map keyed by a
// caller-supplied opaque id; each entry holds the array of strings to
// translate together.
//
//	{
//	  "org_id": 1,
//	  "llm_id": 1234,
//	  "source": "eng",
//	  "target": "spa",
//	  "items": {
//	    "a1f0e2c4-...:text":          ["Hi @contact.name"],
//	    "a1f0e2c4-...:quick_replies": ["Yes", "No"],
//	    "b7d91a22-...:arguments":     ["yes yeah"]
//	  }
//	}
type translateRequest struct {
	OrgID  models.OrgID        `json:"org_id" validate:"required"`
	LLMID  models.LLMID        `json:"llm_id" validate:"required"`
	Source i18n.Language       `json:"source" validate:"required"`
	Target i18n.Language       `json:"target" validate:"required"`
	Items  map[string][]string `json:"items"  validate:"required,min=1"`
}

//	{
//	  "items": {
//	    "a1f0e2c4-...:text": ["Hola @contact.name"]
//	  }
//	}
type translateResponse struct {
	Items map[string][]string `json:"items"`
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
		id     string
		values []string
		err    error
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, translateConcurrency)
	results := make(chan result)

	for id, vals := range r.Items {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			callStart := time.Now()
			translated, tokensUsed, err := translateValues(ctx, llmSvc, instructions, vals)
			llm.RecordCall(rt, time.Since(callStart), tokensUsed)

			results <- result{id: id, values: translated, err: err}
		})
	}

	go func() { wg.Wait(); close(results) }()

	items := make(map[string][]string)
	var svcErr error
	for res := range results {
		if res.err != nil && svcErr == nil {
			svcErr = res.err
		}
		if res.values != nil {
			items[res.id] = res.values
		}
	}

	// An error from the LLM service itself (bad credentials, rate limit, model unavailable, etc.)
	// is reported as 422 because LLMs are user-configured — it's not necessarily our fault.
	if svcErr != nil {
		return nil, 0, svcErr
	}

	return translateResponse{Items: items}, http.StatusOK, nil
}

// translateValues translates a single item's array of strings. Returns the
// translated values, tokens used, and any service error. A nil values slice with
// a nil error means the item was untranslatable (<CANT>, malformed or wrong-length
// JSON response) and should be silently omitted. A non-nil error is an LLM service
// failure and should be reported to the caller as 422.
func translateValues(ctx context.Context, llmSvc flows.LLMService, instructions string, vals []string) ([]string, int64, error) {
	inputBytes, err := json.Marshal(vals)
	if err != nil {
		return nil, 0, nil
	}

	resp, err := llmSvc.Response(ctx, instructions, string(inputBytes), translateMaxTokens)
	if err != nil {
		// context cancellation/deadline is a client/timeout issue, not an LLM config failure
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, err
		}
		// real LLM services wrap their errors as *ai.ServiceError already; wrap anything else
		// (e.g. from the test service) so the handler response is consistently a 422.
		var aerr *ai.ServiceError
		if !errors.As(err, &aerr) {
			err = &ai.ServiceError{Message: err.Error(), Code: ai.ErrorUnknown}
		}
		return nil, 0, err
	}

	if resp.Output == "<CANT>" {
		return nil, resp.TokensUsed, nil
	}

	var translated []string
	if err := json.Unmarshal([]byte(resp.Output), &translated); err != nil {
		return nil, resp.TokensUsed, nil
	}
	if len(translated) != len(vals) {
		return nil, resp.TokensUsed, nil
	}
	return translated, resp.TokensUsed, nil
}
