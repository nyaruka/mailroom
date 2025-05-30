package testdb

import (
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/runtime"
)

type LLM struct {
	ID   models.LLMID
	UUID assets.LLMUUID
}

// InsertLLM inserts an LLM
func InsertLLM(rt *runtime.Runtime, org *Org, uuid assets.LLMUUID, typ string, model, name string, config map[string]any) *LLM {
	var id models.LLMID
	must(rt.DB.Get(&id,
		`INSERT INTO ai_llm(org_id, uuid, llm_type, model, name, config, is_system, is_active, created_on, modified_on, created_by_id, modified_by_id)
		VALUES($1, $2, $3, $4, $5, $6, FALSE, TRUE, NOW(), NOW(), 1, 1) RETURNING id`, org.ID, uuid, typ, model, name, models.JSONB[map[string]any]{config},
	))
	return &LLM{ID: id, UUID: uuid}
}
