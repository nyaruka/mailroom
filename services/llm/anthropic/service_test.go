package anthropic_test

import (
	"io"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/services/llm/anthropic"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	bad := testdb.InsertLLM(t, rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "anthropic", "claude", "Bad Config", map[string]any{}, "TF")
	good := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "anthropic", "claude", "Good", map[string]any{"api_key": "sesame"}, "TF")

	oa := testdb.Org1.Load(t, rt)
	badLLM := oa.LLMByID(bad.ID)
	goodLLM := oa.LLMByID(good.ID)

	client, _ := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.anthropic.com/v1/messages": {
			httpx.NewMockResponse(401, map[string]string{"Content-type": "application/json"}, []byte(`{"type": "error", "error": {"message": "Incorrect API key provided", "type": "invalid_api_key"}}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"type": "error", "error": {"message": "Rate limit reached for your model", "type": "rate_limit_exceeded"}}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"type": "error", "error": {"message": "Rate limit reached for your model", "type": "rate_limit_exceeded"}}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"type": "error", "error": {"message": "Rate limit reached for your model", "type": "rate_limit_exceeded"}}`)),
		},
	})

	// can't create service with bad config
	svc, err := anthropic.New(rt, badLLM, client)
	assert.EqualError(t, err, "config incomplete for LLM: c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc")
	assert.Nil(t, svc)

	svc, err = anthropic.New(rt, goodLLM, client)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	resp, err := svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.ErrorContains(t, err, "Incorrect API key provided")
	var serr *ai.ServiceError
	if assert.ErrorAs(t, err, &serr) {
		assert.Equal(t, ai.ErrorCredentials, serr.Code)
	}
	assert.Nil(t, resp)

	resp, err = svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.ErrorContains(t, err, "Too Many Requests")
	if assert.ErrorAs(t, err, &serr) {
		assert.Equal(t, ai.ErrorRateLimit, serr.Code)
	}
	assert.Nil(t, resp)
}

func TestThinking(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	sonnet5 := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "anthropic", "claude-sonnet-5", "Sonnet 5", map[string]any{"api_key": "sesame"}, "TF")
	opus55 := testdb.InsertLLM(t, rt, testdb.Org1, "2f5a1b56-6f4c-4c67-8d0f-1f2e9a3b7c41", "anthropic", "claude-opus-5-5", "Opus 5.5", map[string]any{"api_key": "sesame"}, "TF")
	oa := testdb.Org1.Load(t, rt)

	okResp := []byte(`{"id":"msg_x","type":"message","role":"assistant","content":[{"type":"text","text":"Hola mundo"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":3}}`)

	tcs := []struct {
		llm      *testdb.LLM
		disabled bool
	}{
		{sonnet5, true}, // thinks by default so we disable it
		{opus55, false}, // can't disable thinking so we leave it alone
	}

	for _, tc := range tcs {
		client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
			"https://api.anthropic.com/v1/messages": {httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, okResp)},
		})

		svc, err := anthropic.New(rt, oa.LLMByID(tc.llm.ID), client)
		require.NoError(t, err)

		resp, err := svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
		require.NoError(t, err)
		assert.Equal(t, "Hola mundo", resp.Output)

		require.Len(t, mocks.Requests(), 1)
		body, err := mocks.Requests()[0].GetBody()
		require.NoError(t, err)
		reqBody, err := io.ReadAll(body)
		require.NoError(t, err)

		if tc.disabled {
			assert.Contains(t, string(reqBody), `"thinking":{"type":"disabled"}`)
		} else {
			assert.NotContains(t, string(reqBody), `"thinking"`)
		}
	}
}

func TestClassify(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	llm := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "anthropic", "claude", "Good", map[string]any{"api_key": "sesame"}, "TF")
	oa := testdb.Org1.Load(t, rt)

	mkResp := func(text string) *httpx.MockResponse {
		return httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"msg_x","type":"message","role":"assistant","content":[{"type":"text","text":"`+text+`"}],"model":"claude","stop_reason":"end_turn","usage":{"input_tokens":34,"output_tokens":2}}`))
	}

	client, _ := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.anthropic.com/v1/messages": {
			mkResp(`{\"Flights\": 0.1, \"Hotels\": 0.85}`),
			mkResp(`{\"Flights\": 0, \"Hotels\": 0}`),
		},
	})

	svc, err := anthropic.New(rt, oa.LLMByID(llm.ID), client)
	require.NoError(t, err)

	cls, err := svc.Classify(ctx, "I need a room", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, &core.Classification{Option: "Hotels", Confidence: 0.85, Probabilities: map[string]float64{"Flights": 0.1, "Hotels": 0.85}, TokensInput: 34, TokensOutput: 2}, cls)

	cls, err = svc.Classify(ctx, "What's the weather?", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, &core.Classification{Option: "Flights", Probabilities: map[string]float64{"Flights": 0, "Hotels": 0}, TokensInput: 34, TokensOutput: 2}, cls)
}
