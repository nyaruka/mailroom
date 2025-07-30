package models_test

import (
	"testing"

	"github.com/nyaruka/goflow/assets"
	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifiers(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshClassifiers)
	require.NoError(t, err)

	classifiers, err := oa.Classifiers()
	require.NoError(t, err)

	tcs := []struct {
		ID      models.ClassifierID
		UUID    assets.ClassifierUUID
		Name    string
		Intents []string
	}{
		{testdb.Luis.ID, testdb.Luis.UUID, "LUIS", []string{"book_flight", "book_car"}},
		{testdb.Wit.ID, testdb.Wit.UUID, "Wit.ai", []string{"register"}},
		{testdb.Bothub.ID, testdb.Bothub.UUID, "BotHub", []string{"intent"}},
	}

	assert.Equal(t, len(tcs), len(classifiers))
	for i, tc := range tcs {
		c := classifiers[i].(*models.Classifier)
		assert.Equal(t, tc.UUID, c.UUID())
		assert.Equal(t, tc.ID, c.ID())
		assert.Equal(t, tc.Name, c.Name())
		assert.Equal(t, tc.Intents, c.Intents())
	}

}
