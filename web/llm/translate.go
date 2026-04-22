package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/mailroom/v26/core/ai/prompts"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/web"
)

func init() {
	web.InternalRoute(http.MethodPost, "/llm/translate", web.JSONPayload(handleTranslate))
}

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

	inputBytes, err := json.Marshal(r.Items)
	if err != nil {
		return nil, 0, fmt.Errorf("error marshaling items: %w", err)
	}

	maxTokens := 500 * totalValues(r.Items)
	if maxTokens < 2500 {
		maxTokens = 2500
	}

	start := time.Now()
	resp, err := llmSvc.Response(ctx, instructions, string(inputBytes), maxTokens)
	if err != nil {
		return nil, 0, fmt.Errorf("error calling LLM service: %w", err)
	}

	llm.RecordCall(rt, time.Since(start), resp.TokensUsed)

	var translated map[string]map[string][]string
	if err := json.Unmarshal([]byte(resp.Output), &translated); err != nil {
		return nil, 0, fmt.Errorf("error parsing LLM response: %w", err)
	}

	items := make(map[string]map[string][]string)
	for id, reqProps := range r.Items {
		respProps, ok := translated[id]
		if !ok {
			continue
		}
		outProps := make(map[string][]string)
		for prop, reqVals := range reqProps {
			respVals, ok := respProps[prop]
			if !ok || len(respVals) != len(reqVals) {
				continue
			}
			outProps[prop] = respVals
		}
		if len(outProps) > 0 {
			items[id] = outProps
		}
	}

	return translateResponse{Items: items}, http.StatusOK, nil
}

func totalValues(items map[string]map[string][]string) int {
	n := 0
	for _, props := range items {
		for _, vals := range props {
			n += len(vals)
		}
	}
	return n
}
