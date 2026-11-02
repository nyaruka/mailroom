package models_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/test/services"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMs(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	gen := testdb.InsertLLM(t, rt, testdb.Org1, "0b4a8ff3-5ab1-4d1c-8e2e-4c5d1f1ec5a1", "test", "gpt-4", "Generation", map[string]any{}, "G")
	cls := testdb.InsertLLM(t, rt, testdb.Org1, "5c7d3f0e-1b8a-4a8c-9f3d-2e6b7a9c1d42", "test", "gpt-4", "Classification", map[string]any{}, "C")
	both := testdb.InsertLLM(t, rt, testdb.Org1, "9e2f6a1b-3c4d-4e5f-8a7b-6c5d4e3f2a13", "test", "gpt-4", "Both", map[string]any{}, "GC")
	unk := testdb.InsertLLM(t, rt, testdb.Org1, "d4c3b2a1-9f8e-4d7c-8b6a-5f4e3d2c1b04", "test", "gpt-4", "Unknown", map[string]any{}, "TX")

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshLLMs)
	require.NoError(t, err)

	llms, err := oa.Models()
	require.NoError(t, err)

	tcs := []struct {
		id    models.LLMID
		uuid  assets.ModelUUID
		name  string
		typ   string
		roles []assets.ModelRole
	}{
		{testdb.OpenAI.ID, testdb.OpenAI.UUID, "GPT-4o", "openai", []assets.ModelRole{assets.ModelRoleTranslate, assets.ModelRoleGenerate, assets.ModelRoleClassify}},
		{testdb.Anthropic.ID, testdb.Anthropic.UUID, "Claude", "anthropic", []assets.ModelRole{assets.ModelRoleTranslate, assets.ModelRoleGenerate, assets.ModelRoleClassify}},
		{testdb.TestLLM.ID, testdb.TestLLM.UUID, "Test", "test", []assets.ModelRole{assets.ModelRoleTranslate, assets.ModelRoleGenerate, assets.ModelRoleClassify}},
		{gen.ID, gen.UUID, "Generation", "test", []assets.ModelRole{assets.ModelRoleGenerate}},
		{cls.ID, cls.UUID, "Classification", "test", []assets.ModelRole{assets.ModelRoleClassify}},
		{both.ID, both.UUID, "Both", "test", []assets.ModelRole{assets.ModelRoleGenerate, assets.ModelRoleClassify}},
		{unk.ID, unk.UUID, "Unknown", "test", []assets.ModelRole{assets.ModelRoleTranslate}},
	}

	assert.Equal(t, len(tcs), len(llms))
	for i, tc := range tcs {
		c := llms[i].(*models.LLM)
		assert.Equal(t, tc.uuid, c.UUID())
		assert.Equal(t, tc.id, c.ID())
		assert.Equal(t, tc.name, c.Name())
		assert.Equal(t, tc.typ, c.Type())
		assert.Equal(t, tc.roles, c.Roles())
		assert.Equal(t, testdb.Org1.ID, c.OrgID())
	}

	assert.Equal(t, "Claude", oa.LLMByID(testdb.Anthropic.ID).Name())
	assert.Nil(t, oa.LLMByID(1235))
}

func TestLLMAsService(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	// register a service type which records the client it's constructed with
	var gotClient *http.Client
	models.RegisterLLMService("test_capture", func(rt *runtime.Runtime, l *models.LLM, c *http.Client) (flows.ModelService, error) {
		gotClient = c
		return services.NewModel(), nil
	})

	llm := &models.LLM{UUID_: "8b3d0b6f-1f45-4b8b-a0b1-2a1e63dc4c9e", Type_: "test_capture"}

	svc, err := llm.AsService(rt)
	require.NoError(t, err)
	assert.NotNil(t, svc)

	// services are always given the client for fixed outbound targets, never one of the caller's choosing,
	// so they get its tracing, timeout and connection pooling
	assert.Same(t, rt.HTTP.Services, gotClient)

	unknown := &models.LLM{UUID_: "0dbf1d3e-2c0f-4c1f-9c6a-9f8c1e4d5b7a", Type_: "nope"}

	_, err = unknown.AsService(rt)
	assert.EqualError(t, err, "unknown type 'nope' for LLM: 0dbf1d3e-2c0f-4c1f-9c6a-9f8c1e4d5b7a")
}

func TestLLMRecordCall(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	llm := oa.LLMByID(testdb.OpenAI.ID)
	require.NotNil(t, llm)

	when := time.Date(2026, 5, 4, 23, 30, 0, 0, time.FixedZone("", -2*3600)) // already the 5th in UTC

	assert.Equal(t, map[string]int64{
		"llms/10000/calls/2026-05-05":      1,
		"llms/10000/tokens:in/2026-05-05":  120,
		"llms/10000/tokens:out/2026-05-05": 340,
	}, llm.RecordCall(rt, when, 250*time.Millisecond, core.ModelTokens{Input: 120, Output: 340}))

	// token counts are only tallied when the service reports them
	assert.Equal(t, map[string]int64{"llms/10000/calls/2026-05-05": 1}, llm.RecordCall(rt, when, 250*time.Millisecond, core.ModelTokens{}))
}

func TestLLMMaxOutputTokens(t *testing.T) {
	tcs := []struct {
		configured int
		expected   int
	}{
		{4096, 4096},
		{16000, 16000},
		{16001, 16000},
		{128000, 16000},
	}

	for _, tc := range tcs {
		l := &models.LLM{MaxOutputTokens_: tc.configured}
		assert.Equal(t, tc.expected, l.MaxOutputTokens(), "configured=%d", tc.configured)
	}
}
