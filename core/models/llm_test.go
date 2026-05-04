package models_test

import (
	"testing"
	"time"

	"github.com/nyaruka/gocommon/dates"
	"github.com/nyaruka/gocommon/dbutil/assertdb"
	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/goflow/flows"
	"github.com/nyaruka/goflow/flows/events"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMs(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshLLMs)
	require.NoError(t, err)

	llms, err := oa.LLMs()
	require.NoError(t, err)

	tcs := []struct {
		id    models.LLMID
		uuid  assets.LLMUUID
		name  string
		typ   string
		roles []assets.LLMRole
	}{
		{testdb.OpenAI.ID, testdb.OpenAI.UUID, "GPT-4o", "openai", []assets.LLMRole{assets.LLMRoleEditing, assets.LLMRoleEngine}},
		{testdb.Anthropic.ID, testdb.Anthropic.UUID, "Claude", "anthropic", []assets.LLMRole{assets.LLMRoleEditing, assets.LLMRoleEngine}},
		{testdb.TestLLM.ID, testdb.TestLLM.UUID, "Test", "test", []assets.LLMRole{assets.LLMRoleEditing, assets.LLMRoleEngine}},
	}

	assert.Equal(t, len(tcs), len(llms))
	for i, tc := range tcs {
		c := llms[i].(*models.LLM)
		assert.Equal(t, tc.uuid, c.UUID())
		assert.Equal(t, tc.id, c.ID())
		assert.Equal(t, tc.name, c.Name())
		assert.Equal(t, tc.typ, c.Type())
		assert.Equal(t, tc.roles, c.Roles())
	}

	assert.Equal(t, "Claude", oa.LLMByID(testdb.Anthropic.ID).Name())
	assert.Nil(t, oa.LLMByID(1235))
}

func TestLLMRecordCall(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	dates.SetNowFunc(dates.NewFixedNow(time.Date(2026, 5, 4, 13, 14, 30, 0, time.UTC)))
	defer dates.SetNowFunc(time.Now)

	oa, err := models.GetOrgAssets(ctx, rt, testdb.Org1.ID)
	require.NoError(t, err)

	llm := oa.LLMByID(testdb.OpenAI.ID)
	require.NotNil(t, llm)

	mkEvent := func(in, out int64) *events.LLMCalled {
		return events.NewLLMCalled(flows.NewLLM(llm), "instructions", "input", &flows.LLMResponse{Output: "output", TokensInput: in, TokensOutput: out}, 250*time.Millisecond)
	}

	assert.Len(t, llm.RecordCall(rt, oa, mkEvent(120, 340)), 3)
	assert.Len(t, llm.RecordCall(rt, oa, mkEvent(80, 200)), 3)
	assert.Len(t, llm.RecordCall(rt, oa, mkEvent(0, 0)), 1)

	var allCounts []*models.LLMDailyCount
	allCounts = append(allCounts, llm.RecordCall(rt, oa, mkEvent(120, 340))...)
	allCounts = append(allCounts, llm.RecordCall(rt, oa, mkEvent(80, 200))...)
	allCounts = append(allCounts, llm.RecordCall(rt, oa, mkEvent(0, 0))...)

	require.NoError(t, models.InsertLLMDailyCounts(ctx, rt.DB, allCounts))

	assertdb.Query(t, rt.DB, `SELECT COUNT(*) FROM ai_llmcount WHERE llm_id = $1`, testdb.OpenAI.ID).Returns(7)
	assertdb.Query(t, rt.DB, `SELECT COALESCE(SUM(count), 0)::bigint FROM ai_llmcount WHERE llm_id = $1 AND scope = 'calls'`, testdb.OpenAI.ID).Returns(int64(3))
	assertdb.Query(t, rt.DB, `SELECT COALESCE(SUM(count), 0)::bigint FROM ai_llmcount WHERE llm_id = $1 AND scope = 'tokens:in'`, testdb.OpenAI.ID).Returns(int64(200))
	assertdb.Query(t, rt.DB, `SELECT COALESCE(SUM(count), 0)::bigint FROM ai_llmcount WHERE llm_id = $1 AND scope = 'tokens:out'`, testdb.OpenAI.ID).Returns(int64(540))
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
