package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/nyaruka/gocommon/i18n"
	"github.com/nyaruka/mailroom/core/ai"
	"github.com/nyaruka/mailroom/core/ai/prompts"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
	"github.com/nyaruka/mailroom/web"
)

func init() {
	web.RegisterRoute(http.MethodPost, "/mr/llm/translate", web.RequireAuthToken(web.JSONPayload(handleTranslate)))
}

// Performs translation using an LLM.
//
//	{
//	  "org_id": 1,
//	  "llm_id": 1234,
//	  "from_language": "eng",
//	  "to_language": "spa",
//	  "text": "Hello world"
//	}
type translateRequest struct {
	OrgID        models.OrgID  `json:"org_id"        validate:"required"`
	LLMID        models.LLMID  `json:"llm_id"        validate:"required"`
	FromLanguage i18n.Language `json:"from_language" validate:"required"`
	ToLanguage   i18n.Language `json:"to_language"   validate:"required"`
	Text         string        `json:"text"          validate:"required"`
}

//	{
//	  "text": "Hola mundo",
//	  "tokens_used": 123
//	}
type translateResponse struct {
	Text       string `json:"text"`
	TokensUsed int64  `json:"tokens_used,omitempty"`
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

	llmSvc, err := llm.AsService()
	if err != nil {
		return nil, 0, fmt.Errorf("error creating LLM service: %w", err)
	}

	instructionsTpl := "translate"
	if r.FromLanguage == "und" || r.FromLanguage == "mul" {
		instructionsTpl = "translate_unknown_from"
	}

	instructions := prompts.Render(instructionsTpl, r)
	start := time.Now()

	resp, err := llmSvc.Response(ctx, instructions, r.Text, 2500)
	if err != nil {
		return nil, 0, fmt.Errorf("error calling LLM service: %w", err)
	}

	llm.RecordCall(rt, time.Since(start), resp.TokensUsed)

	if resp.Output == "<CANT>" {
		return nil, 0, &ai.ServiceError{Message: "unable to perform translation", Code: ai.ErrorReasoning, Instructions: instructions, Input: r.Text}
	}

	return translateResponse{Text: resp.Output, TokensUsed: resp.TokensUsed}, http.StatusOK, nil
}
