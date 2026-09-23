package openai_azure_test

import (
	"io"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/services/llm/openai_azure"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	bad := testdb.InsertLLM(t, rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "openai_azure", "gpt-4", "Bad Config", map[string]any{}, "TF")
	good := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai_azure", "gpt-4", "Good", map[string]any{"endpoint": "http://azure.com/ai", "api_key": "sesame"}, "TF")

	oa := testdb.Org1.Load(t, rt)
	badLLM := oa.LLMByID(bad.ID)
	goodLLM := oa.LLMByID(good.ID)

	client, _ := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"http://azure.com/ai/openai/deployments/gpt-4/chat/completions?api-version=2025-03-01-preview": {
			httpx.NewMockResponse(401, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Incorrect API key provided", "type": "invalid_request_error", "param": null, "code": "invalid_api_key"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
		},
	})

	// can't create service with bad config
	svc, err := openai_azure.New(rt, badLLM, client)
	assert.EqualError(t, err, "config incomplete for LLM: c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc")
	assert.Nil(t, svc)

	svc, err = openai_azure.New(rt, goodLLM, client)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	resp, err := svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.ErrorContains(t, err, "Unauthorized")
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

func TestClassify(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	llm := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai_azure", "gpt-4", "Good", map[string]any{"api_key": "sesame", "endpoint": "http://azure.com/ai"}, "TF")
	oa := testdb.Org1.Load(t, rt)

	mkResp := func(content, logprobs string) *httpx.MockResponse {
		return httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1741476542,"model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"`+content+`"},"logprobs":`+logprobs+`,"finish_reason":"stop"}],"usage":{"prompt_tokens":34,"completion_tokens":2,"total_tokens":36}}`))
	}

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"http://azure.com/ai/openai/deployments/gpt-4/chat/completions?api-version=2025-03-01-preview": {
			mkResp("Hotels", `{"content":[{"token":"Hot","logprob":-0.1,"bytes":null,"top_logprobs":[]},{"token":"els","logprob":-0.05,"bytes":null,"top_logprobs":[]}],"refusal":null}`),
			mkResp("<CANT>", `null`),
		},
	})

	svc, err := openai_azure.New(rt, oa.LLMByID(llm.ID), client)
	require.NoError(t, err)

	cls, err := svc.Classify(ctx, "I need a room", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, "Hotels", cls.Option)
	assert.InDelta(t, 0.8607, cls.Confidence, 0.0001)
	assert.Equal(t, int64(34), cls.TokensInput)
	assert.Equal(t, int64(2), cls.TokensOutput)

	body, err := mocks.Requests()[0].GetBody()
	require.NoError(t, err)
	reqBody, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Contains(t, string(reqBody), `"logprobs":true`)

	cls, err = svc.Classify(ctx, "What's the weather?", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	assert.EqualError(t, err, "no option fits input")
	assert.Nil(t, cls)
}
