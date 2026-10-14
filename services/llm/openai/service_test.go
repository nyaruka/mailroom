package openai_test

import (
	"io"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/goflow/core"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/core/ai"
	"github.com/nyaruka/mailroom/v26/services/llm/openai"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	bad := testdb.InsertLLM(t, rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "openai", "gpt-4", "Bad Config", map[string]any{}, "TGC")
	good := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai", "gpt-4", "Good", map[string]any{"api_key": "sesame"}, "TGC")

	oa := testdb.Org1.Load(t, rt)
	badLLM := oa.LLMByID(bad.ID)
	goodLLM := oa.LLMByID(good.ID)

	client, _ := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.openai.com/v1/responses": {
			httpx.NewMockResponse(401, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Incorrect API key provided", "type": "invalid_request_error", "param": null, "code": "invalid_api_key"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{
				"id": "resp_67ccd2bed1ec8190b14f964abc0542670bb6a6b452d3795b", 
				"object": "response", 
				"created_at": 1741476542, 
				"status": "completed", 
				"error": null,
				"output": [
					{
						"type": "message",
						"id": "msg_67ccd2bf17f0819081ff3bb2cf6508e60bb6a6b452d3795b",
						"status": "completed",
						"role": "assistant",
						"content": [
							{
								"type": "output_text",
								"text": "Hola mundo",
								"annotations": []
							}
						]
					}
				],
				"parallel_tool_calls": true,
				"previous_response_id": null,
				"reasoning": {
					"effort": null,
					"summary": null
				},
				"store": true,
				"temperature": 1.0,
				"text": {
					"format": {
						"type": "text"
					}
				},
				"tool_choice": "auto",
				"tools": [],
				"top_p": 1.0,
				"truncation": "disabled",
				"usage": {
					"input_tokens": 36,
					"input_tokens_details": {
						"cached_tokens": 0
					},
					"output_tokens": 87,
					"output_tokens_details": {
						"reasoning_tokens": 0
					},
					"total_tokens": 123
				},
				"user": null,
				"metadata": {}
			}`)),
		},
	})

	// can't create service with bad config
	svc, err := openai.New(rt, badLLM, client)
	assert.EqualError(t, err, "config incomplete for LLM: c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc")
	assert.Nil(t, svc)

	svc, err = openai.New(rt, goodLLM, client)
	assert.NoError(t, err)
	assert.NotNil(t, svc)

	resp, err := svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.EqualError(t, err, "POST \"https://api.openai.com/v1/responses\": 401 Unauthorized ")
	var serr *ai.ServiceError
	if assert.ErrorAs(t, err, &serr) {
		assert.Equal(t, ai.ErrorCredentials, serr.Code)
	}
	assert.Nil(t, resp)

	resp, err = svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.EqualError(t, err, "POST \"https://api.openai.com/v1/responses\": 429 Too Many Requests ")
	if assert.ErrorAs(t, err, &serr) {
		assert.Equal(t, ai.ErrorRateLimit, serr.Code)
	}
	assert.Nil(t, resp)

	resp, err = svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.NoError(t, err)
	assert.Equal(t, "Hola mundo", resp.Output)
	assert.Equal(t, int64(36), resp.Tokens.Input)
	assert.Equal(t, int64(87), resp.Tokens.Output)
}

func TestClassify(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	llm := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai", "gpt-5.5", "Good", map[string]any{"api_key": "sesame"}, "TGC")
	oa := testdb.Org1.Load(t, rt)

	mkResp := func(text, logprobs string) *httpx.MockResponse {
		return httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"`+text+`","annotations":[],"logprobs":`+logprobs+`}]}],"usage":{"input_tokens":34,"output_tokens":2}}`))
	}

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.openai.com/v1/responses": {
			mkResp("Hotels", `[{"token":"Hot","logprob":-0.1,"top_logprobs":[]},{"token":"els","logprob":-0.05,"top_logprobs":[]}]`),
			mkResp("Hotels", `[]`),
			mkResp("<CANT>", `[]`),
		},
	})

	svc, err := openai.New(rt, oa.LLMByID(llm.ID), client)
	require.NoError(t, err)

	cls, err := svc.Classify(ctx, "I need a room", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, "Hotels", cls.Option)
	assert.Equal(t, core.ClassifierConfidenceMedium, cls.Confidence)
	assert.Equal(t, int64(34), cls.Tokens.Input)
	assert.Equal(t, int64(2), cls.Tokens.Output)

	reqBody := requestBody(t, mocks, 0)
	assert.Contains(t, reqBody, `"include":["message.output_text.logprobs"]`)
	assert.Contains(t, reqBody, `"max_output_tokens":50`)
	assert.NotContains(t, reqBody, `"temperature"`)
	assert.Contains(t, reqBody, `"reasoning":{"effort":"none"}`)

	// no logprobs returned
	cls, err = svc.Classify(ctx, "I need a room", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, "Hotels", cls.Option)
	assert.Equal(t, ai.UnscoredConfidence, cls.Confidence)

	// none of the options fit
	cls, err = svc.Classify(ctx, "What's the weather?", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, &core.Classification{Option: "Flights", Confidence: core.ClassifierConfidenceNone, Tokens: core.ModelTokens{Input: 34, Output: 2}}, cls)
}

func TestReasoningModel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	llm := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai", "gpt-5.6-luna", "Good", map[string]any{"api_key": "sesame"}, "TGC")
	oa := testdb.Org1.Load(t, rt)

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.openai.com/v1/responses": {
			httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hola mundo","annotations":[]}]}],"usage":{"input_tokens":36,"output_tokens":3}}`)),
			httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"resp_2","object":"response","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"reasoning","id":"rs_1","summary":[]}],"usage":{"input_tokens":36,"output_tokens":50}}`)),
		},
	})

	svc, err := openai.New(rt, oa.LLMByID(llm.ID), client)
	require.NoError(t, err)

	resp, err := svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	require.NoError(t, err)
	assert.Equal(t, "Hola mundo", resp.Output)
	assert.Contains(t, requestBody(t, mocks, 0), `"reasoning":{"effort":"none"}`)

	// output token limit reached before any output
	resp, err = svc.Response(ctx, "translate to Spanish", "Hello world", 1000)
	assert.EqualError(t, err, "response incomplete with no output: max_output_tokens")
	var serr *ai.ServiceError
	if assert.ErrorAs(t, err, &serr) {
		assert.Equal(t, ai.ErrorUnknown, serr.Code)
	}
	assert.Nil(t, resp)
}

func TestReasoningCantBeTurnedOff(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	llm := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai", "gpt-6-astra", "Good", map[string]any{"api_key": "sesame"}, "TGC")
	oa := testdb.Org1.Load(t, rt)

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.openai.com/v1/responses": {
			httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"{\"Flights\": 0.05, \"Hotels\": 0.95}","annotations":[]}]}],"usage":{"input_tokens":34,"output_tokens":12}}`)),
		},
	})

	svc, err := openai.New(rt, oa.LLMByID(llm.ID), client)
	require.NoError(t, err)

	// classifies by prompt with the lowest effort since the model can't provide logprobs while reasoning
	cls, err := svc.Classify(ctx, "I need a room", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, "Hotels", cls.Option)
	assert.Equal(t, core.ClassifierConfidenceHigh, cls.Confidence)

	assert.Contains(t, requestBody(t, mocks, 0), `"reasoning":{"effort":"low"}`)
	assert.NotContains(t, requestBody(t, mocks, 0), `"include"`)
}

func TestNonReasoningModel(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	llm := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai", "gpt-4o", "Good", map[string]any{"api_key": "sesame"}, "TGC")
	oa := testdb.Org1.Load(t, rt)

	client, mocks := test.MockedHTTP(map[string][]*httpx.MockResponse{
		"https://api.openai.com/v1/responses": {
			httpx.NewMockResponse(200, map[string]string{"Content-type": "application/json"}, []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hotels","annotations":[],"logprobs":[{"token":"Hotels","logprob":-0.01,"top_logprobs":[]}]}]}],"usage":{"input_tokens":34,"output_tokens":1}}`)),
		},
	})

	svc, err := openai.New(rt, oa.LLMByID(llm.ID), client)
	require.NoError(t, err)

	cls, err := svc.Classify(ctx, "I need a room", []*core.ClassifierOption{{Name: "Flights"}, {Name: "Hotels"}})
	require.NoError(t, err)
	assert.Equal(t, "Hotels", cls.Option)
	assert.Equal(t, core.ClassifierConfidenceHigh, cls.Confidence)

	assert.NotContains(t, requestBody(t, mocks, 0), `"reasoning"`)
	assert.Contains(t, requestBody(t, mocks, 0), `"include":["message.output_text.logprobs"]`)
}

func requestBody(t *testing.T, mocks *httpx.MocksTransport, i int) string {
	t.Helper()

	body, err := mocks.Requests()[i].GetBody()
	require.NoError(t, err)
	b, err := io.ReadAll(body)
	require.NoError(t, err)
	return string(b)
}
