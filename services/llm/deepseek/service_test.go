package deepseek_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/mailroom/core/ai"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/services/llm/deepseek"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestService(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	bad := testdb.InsertLLM(rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "deepseek", "chat", "Bad Config", map[string]any{})
	good := testdb.InsertLLM(rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "deepseek", "chat", "Good", map[string]any{"api_key": "sesame"})
	models.FlushCache()

	oa := testdb.Org1.Load(rt)
	badLLM := oa.LLMByID(bad.ID)
	goodLLM := oa.LLMByID(good.ID)

	client := &http.Client{Transport: httpx.NewMockRequestor(map[string][]*httpx.MockResponse{
		"https://api.deepseek.com/chat/completions": {
			httpx.NewMockResponse(401, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Incorrect API key provided", "type": "invalid_request_error", "param": null, "code": "invalid_api_key"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
			httpx.NewMockResponse(429, map[string]string{"Content-type": "application/json"}, []byte(`{"message": "Rate limit reached for your model", "type": "requests", "param": null, "code": "rate_limit_exceeded"}`)),
		},
	})}

	// can't create service with bad config
	svc, err := deepseek.New(badLLM, client)
	assert.EqualError(t, err, "config incomplete for LLM: c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc")
	assert.Nil(t, svc)

	svc, err = deepseek.New(goodLLM, client)
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
