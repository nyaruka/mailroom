package google_test

import (
	"net/http"
	"testing"

	"github.com/nyaruka/mailroom/services/llm/google"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"
	"github.com/stretchr/testify/assert"
)

func TestService(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetData)

	bad := testdb.InsertLLM(t, rt, testdb.Org1, "c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc", "google", "gemini", "Bad Config", map[string]any{})
	good := testdb.InsertLLM(t, rt, testdb.Org1, "b86966fd-206e-4bdd-a962-06faa3af1182", "google", "gemini", "Good", map[string]any{"api_key": "sesame"})

	oa := testdb.Org1.Load(t, rt)
	badLLM := oa.LLMByID(bad.ID)
	goodLLM := oa.LLMByID(good.ID)

	// can't create service with bad config
	svc, err := google.New(badLLM, http.DefaultClient)
	assert.EqualError(t, err, "config incomplete for LLM: c69723d8-fb37-4cf6-9ec4-bc40cb36f2cc")
	assert.Nil(t, svc)

	svc, err = google.New(goodLLM, http.DefaultClient)
	assert.NoError(t, err)
	assert.NotNil(t, svc)
}
