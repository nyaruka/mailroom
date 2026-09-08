package models_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbedding(t *testing.T) {
	// nil embedding writes as NULL
	v, err := models.Embedding(nil).Value()
	assert.NoError(t, err)
	assert.Nil(t, v)

	v, err = models.Embedding{0.5, -1.25, 3}.Value()
	assert.NoError(t, err)
	assert.Equal(t, "[0.5,-1.25,3]", v)

	var e models.Embedding
	assert.NoError(t, e.Scan([]byte(`[0.5,-1.25,3]`)))
	assert.Equal(t, models.Embedding{0.5, -1.25, 3}, e)

	// can scan from a string, with spaces and exponents
	assert.NoError(t, e.Scan(`[ 1e-07, 2 ]`))
	assert.Equal(t, models.Embedding{1e-07, 2}, e)

	// scanning NULL gives nil
	assert.NoError(t, e.Scan(nil))
	assert.Nil(t, e)

	// values round trip without losing precision
	e1 := models.Embedding{0.1234567, -0.9999999, 1.5e-10, 42}
	v, err = e1.Value()
	require.NoError(t, err)
	var e2 models.Embedding
	require.NoError(t, e2.Scan([]byte(v.(string))))
	assert.Equal(t, e1, e2)

	// errors for unsupported types and malformed values
	assert.EqualError(t, e.Scan(42), "failed type assertion to []byte or string: int")
	assert.EqualError(t, e.Scan([]byte(`0.5,1`)), "0.5,1 is not a valid vector value")
	assert.ErrorContains(t, e.Scan([]byte(`[a,b]`)), "error parsing vector value")
}
