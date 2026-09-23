package knowledge_test

import (
	"testing"

	"github.com/nyaruka/mailroom/v26/core/knowledge"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/testsuite"
	"github.com/nyaruka/mailroom/v26/testsuite/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// creates an embedding with the given leading components, sized for the vector column chunks are stored in
func testEmbedding(vals ...float32) models.Embedding {
	return models.Embedding(testsuite.MockEmbedding(vals...))
}

func TestSearch(t *testing.T) {
	ctx, rt := testsuite.Runtime(t)

	oa := testdb.Org1.Load(t, rt)

	// the query embeds to the same vector as the Refunds chunk so scores are predictable
	embedder := &testsuite.MockEmbedder{Vectors: map[string][]float32{"how do refunds work?": testEmbedding(1, 0)}}
	rt.Embeddings = embedder

	// a ready source with chunks at right angles to each other so their scores are predictable
	k1 := testdb.InsertKnowledge(t, rt, testdb.Org1, "5384b1c6-1099-4a5f-a005-9d3a4092c5c1", models.KnowledgeTypeShortcuts, "Test Shortcuts", models.KnowledgeStatusReady)
	testdb.InsertKnowledgeChunk(t, rt, k1, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", "Refunds", "We offer full refunds within 30 days.", testEmbedding(1, 0))
	testdb.InsertKnowledgeChunk(t, rt, k1, "0e2e1c66-c221-4726-a08a-1a4bbabf05be", "Greeting", "Hello! How can we help?", testEmbedding(1, 1))
	testdb.InsertKnowledgeChunk(t, rt, k1, "b26e0a76-9d88-42d1-9bc9-5cf25e2ba18f", "Hours", "We're open 9 to 5.", testEmbedding(0, 1))

	// chunks in a source that isn't ready aren't searchable
	k2 := testdb.InsertKnowledge(t, rt, testdb.Org1, "78bee0eb-a3d1-4e2b-b91b-6ee1c2f1ab19", models.KnowledgeTypeShortcuts, "Pending", models.KnowledgeStatusPending)
	testdb.InsertKnowledgeChunk(t, rt, k2, "9f0b4b7c-3a17-4f5e-95a8-4d68f21e2a7d", "Nope", "Not indexed yet.", testEmbedding(1, 0))

	// and neither are chunks belonging to another org
	k3 := testdb.InsertKnowledge(t, rt, testdb.Org2, "df22cbcb-e0e1-4e78-be9f-2e4fbea1b2c3", models.KnowledgeTypeShortcuts, "Other Org", models.KnowledgeStatusReady)
	testdb.InsertKnowledgeChunk(t, rt, k3, "0a1c6a9a-52ed-40cb-a921-1a29b9d8bc6f", "Nope", "Other org's content.", testEmbedding(1, 0))

	results, err := knowledge.Search(ctx, rt, oa, "how do refunds work?", nil, 10)
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, models.KnowledgeUUID("5384b1c6-1099-4a5f-a005-9d3a4092c5c1"), results[0].KnowledgeUUID)
	assert.Equal(t, "8d40e9ab-c5f1-4b24-b60f-bc42cf65a9f5", string(results[0].ItemKey))
	assert.Equal(t, "Refunds", results[0].ItemName)
	assert.Equal(t, "We offer full refunds within 30 days.", results[0].Text)
	assert.InDelta(t, 1.0, results[0].Score, 0.001)

	assert.Equal(t, "Greeting", results[1].ItemName)
	assert.InDelta(t, 0.7071, results[1].Score, 0.001)

	assert.Equal(t, "Hours", results[2].ItemName)
	assert.InDelta(t, 0.0, results[2].Score, 0.001)

	// limit caps the number of results
	results, err = knowledge.Search(ctx, rt, oa, "how do refunds work?", nil, 2)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "Refunds", results[0].ItemName)
	assert.Equal(t, "Greeting", results[1].ItemName)

	// a second ready source whose chunk scores higher than anything in the first
	k4 := testdb.InsertKnowledge(t, rt, testdb.Org1, "c1f5d6a3-8e2b-4f7c-9a0d-3b6e1f2a4c5d", models.KnowledgeTypeHelpdesk, "Test Helpdesk", models.KnowledgeStatusReady)
	testdb.InsertKnowledgeChunk(t, rt, k4, "e7a2b9c4-1d3f-4e5a-8b6c-9d0e1f2a3b4c", "Refund Policy", "Refunds take 5 days.", testEmbedding(1, 0))

	results, err = knowledge.Search(ctx, rt, oa, "how do refunds work?", nil, 10)
	require.NoError(t, err)
	assert.Len(t, results, 4)

	// searching only the first source leaves out the second's chunks
	results, err = knowledge.Search(ctx, rt, oa, "how do refunds work?", []models.KnowledgeUUID{k1.UUID}, 10)
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.Equal(t, k1.UUID, r.KnowledgeUUID)
	}

	// and searching only the second leaves out the first's
	results, err = knowledge.Search(ctx, rt, oa, "how do refunds work?", []models.KnowledgeUUID{k4.UUID}, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Refund Policy", results[0].ItemName)

	// naming a source that isn't searchable - here one that isn't ready, or is another org's - doesn't widen the search
	results, err = knowledge.Search(ctx, rt, oa, "how do refunds work?", []models.KnowledgeUUID{k2.UUID, k3.UUID}, 10)
	require.NoError(t, err)
	assert.Empty(t, results)

	// the query was embedded as a query, not as a passage
	assert.Len(t, embedder.Queries, 6)
	assert.Equal(t, "how do refunds work?", embedder.Queries[0])
	assert.Empty(t, embedder.Passages)
}
