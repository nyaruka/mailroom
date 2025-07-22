package models_test

import (
	"testing"

	"github.com/nyaruka/mailroom/core/models"
	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/mailroom/testsuite/testdb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTopics(t *testing.T) {
	ctx, rt := testsuite.Runtime()

	oa, err := models.GetOrgAssetsWithRefresh(ctx, rt, testdb.Org1.ID, models.RefreshTopics)
	require.NoError(t, err)

	topics, err := oa.Topics()
	require.NoError(t, err)

	assert.Equal(t, 3, len(topics))
	assert.Equal(t, testdb.DefaultTopic.ID, topics[0].(*models.Topic).ID())
	assert.Equal(t, testdb.DefaultTopic.UUID, topics[0].UUID())
	assert.Equal(t, "General", topics[0].Name())
	assert.Equal(t, testdb.SalesTopic.ID, topics[1].(*models.Topic).ID())
	assert.Equal(t, testdb.SalesTopic.UUID, topics[1].UUID())
	assert.Equal(t, "Sales", topics[1].Name())
	assert.Equal(t, testdb.SupportTopic.UUID, topics[2].UUID())
	assert.Equal(t, "Support", topics[2].Name())

	assert.Equal(t, topics[1], oa.TopicByID(testdb.SalesTopic.ID))
	assert.Equal(t, topics[2], oa.TopicByUUID(testdb.SupportTopic.UUID))
}
