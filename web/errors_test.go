package web_test

import (
	"errors"
	"testing"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/contactql"
	"github.com/nyaruka/goflow/envs"
	"github.com/nyaruka/mailroom/v26/web"
	"github.com/stretchr/testify/assert"
)

func TestErrorToResponse(t *testing.T) {
	// create a simple error
	resp, status := web.ErrorToResponse(errors.New("I'm an error!"))
	assert.Equal(t, "I'm an error!", resp.Error)
	assert.Equal(t, 500, status)

	er1JSON, err := jsonx.Marshal(resp)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"error": "I'm an error!"}`, string(er1JSON))

	// create a query error
	_, err = contactql.ParseQuery(envs.NewBuilder().Build(), "$$", nil)

	resp, status = web.ErrorToResponse(err)
	assert.Equal(t, "mismatched input '$' expecting {'(', STRING, PROPERTY, TEXT}", resp.Error)
	assert.Equal(t, "query:syntax", resp.Code)
	assert.Equal(t, 422, status)

	er2JSON, err := jsonx.Marshal(resp)
	assert.NoError(t, err)
	assert.JSONEq(t, `{"error": "mismatched input '$' expecting {'(', STRING, PROPERTY, TEXT}", "code": "query:syntax"}`, string(er2JSON))
}
