package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/llm/translate", web.JSONPayload(handleTranslate))
}

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

	// Flatten all items into a single array for one LLM call, in deterministic
	// id order so the response can be reliably split back into per-id slices.
	ids := make([]string, 0, len(r.Items))
	for id := range r.Items {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	flat := make([]string, 0)
	for _, id := range ids {
		flat = append(flat, r.Items[id]...)
	}

	inputBytes, err := json.Marshal(flat)
	if err != nil {
		return nil, 0, fmt.Errorf("error marshaling input: %w", err)
	}

	callStart := time.Now()
	resp, err := llmSvc.Response(ctx, instructions, string(inputBytes), llm.MaxOutputTokens())
	var tokensUsed int64
	if resp != nil {
		tokensUsed = resp.TokensUsed
	}
	llm.RecordCall(rt, time.Since(callStart), tokensUsed)

	// An error from the LLM service itself (bad credentials, rate limit, model unavailable, etc.)
	// is reported as 422 because LLMs are user-configured — it's not necessarily our fault.
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

	// A <CANT>, malformed response, or wrong-length response means nothing is
	// translatable and is silently reported back as an empty items map.
	items := make(map[string][]string)
	if resp.Output == "<CANT>" {
		return translateResponse{Items: items}, http.StatusOK, nil
	}

	var translated []string
	if err := json.Unmarshal([]byte(resp.Output), &translated); err != nil {
		return translateResponse{Items: items}, http.StatusOK, nil
	}
	if len(translated) != len(flat) {
		return translateResponse{Items: items}, http.StatusOK, nil
	}

	offset := 0
	for _, id := range ids {
		n := len(r.Items[id])
		items[id] = translated[offset : offset+n]
		offset += n
	}

	return translateResponse{Items: items}, http.StatusOK, nil
}
