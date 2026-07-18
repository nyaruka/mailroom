package utils_test

import (
	"testing"

	"github.com/nyaruka/gocommon/random"
	"github.com/nyaruka/mailroom/v26/utils"
	"github.com/stretchr/testify/assert"
)

func TestRandomBase64(t *testing.T) {
	defer random.SetGenerator(random.DefaultGenerator)
	random.SetGenerator(random.NewSeededGenerator(123))

	assert.Equal(t, "W+1GTMx3lX", utils.RandomBase64(10))
	assert.Equal(t, "L/folWKCTh", utils.RandomBase64(10))
	assert.Len(t, utils.RandomBase64(20), 20)
}
