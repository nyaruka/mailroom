package openai_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/core/ai"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/services/llm/openai"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestService(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	defer testsuite.Reset(t, testsuite.ResetData)

	bad := testdb.InsertLLM(rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "openai", "gpt-4", "Bad Config", map[string]any{})
	good := testdb.InsertLLM(rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "openai", "gpt-4", "Good", map[string]any{"api_key": "sesame"})
	models.FlushCache()

	oa := testdb.Org1.Load(rt)
	badLLM := oa.LLMByID(bad.ID)
	goodLLM := oa.LLMByID(good.ID)

	client := &http.Client{Transport: httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
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
	})}

	// can't create service with bad config
	svc, err := openai.New(badLLM, client)
	assert.EqualError(t, err, "config incomplete for LLM: c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc")
	assert.Nil(t, svc)

	svc, err = openai.New(goodLLM, client)
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
	assert.Equal(t, int64(123), resp.TokensUsed)
}
