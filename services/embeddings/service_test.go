package embeddings_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/nyaruka/gocommon/httpx"
	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/goflow/test"
	"github.com/nyaruka/mailroom/v26/services/embeddings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testModel = "intfloat/multilingual-e5-small"

// creates a service talking to the given mocked transport - these tests need no database or runtime
func testService(mocks *httpx.MocksTransport) *embeddings.Service {
	return embeddings.NewService(&http.Client{Transport: mocks}, "http://embeddings:8095/v1", testModel)
}

// creates a response body of n single-value embeddings, [offset], [offset+1], ...
func embeddingsBody(n, offset int) []byte {
	data := make([]map[string]any, n)
	for i := range n {
		data[i] = map[string]any{"index": i, "embedding": []float32{float32(offset + i)}}
	}
	return jsonx.MustMarshal(map[string]any{"data": data})
}

func TestEmbedQuery(t *testing.T) {
	ctx := context.Background()

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			httpx.NewMockResponse(200, nil, []byte(`{"data": [{"index": 0, "embedding": [0.1, 0.2, 0.3]}]}`)),
			httpx.NewMockResponse(500, nil, []byte(`{"error": "oops"}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"data": []}`)),
		},
	})
	svc := testService(mocks)

	e, err := svc.EmbedQuery(ctx, "how do I reset my password?")
	assert.NoError(t, err)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, e)

	// check the query prefix was added inside the service
	body, err := io.ReadAll(mocks.Requests()[0].Body)
	require.NoError(t, err)
	test.AssertEqualJSON(t, []byte(`{
		"model": "intfloat/multilingual-e5-small",
		"input": ["query: how do I reset my password?"]
	}`), body)

	_, err = svc.EmbedQuery(ctx, "hello")
	assert.EqualError(t, err, `error calling embeddings endpoint, got non-200 status: {"error": "oops"}`)

	// a response with the wrong number of embeddings is an error
	_, err = svc.EmbedQuery(ctx, "hello")
	assert.EqualError(t, err, `embeddings response contains 0 embeddings for 1 inputs`)

	assert.False(t, mocks.HasUnused())
}

func TestEmbedPassages(t *testing.T) {
	ctx := context.Background()

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			// embeddings can be returned out of order and are matched back to inputs by index
			httpx.NewMockResponse(200, nil, []byte(`{"data": [{"index": 1, "embedding": [2]}, {"index": 0, "embedding": [1]}]}`)),
		},
	})
	svc := testService(mocks)

	es, err := svc.EmbedPassages(ctx, []string{"To reset your password...", "Our refund policy..."})
	assert.NoError(t, err)
	assert.Equal(t, [][]float32{{1}, {2}}, es)

	// check the passage prefixes were added inside the service
	body, err := io.ReadAll(mocks.Requests()[0].Body)
	require.NoError(t, err)
	test.AssertEqualJSON(t, []byte(`{
		"model": "intfloat/multilingual-e5-small",
		"input": ["passage: To reset your password...", "passage: Our refund policy..."]
	}`), body)

	// no texts means no requests
	es, err = svc.EmbedPassages(ctx, nil)
	assert.NoError(t, err)
	assert.Len(t, es, 0)

	assert.False(t, mocks.HasUnused())
}

func TestEmbedPassagesBadIndexes(t *testing.T) {
	ctx := context.Background()

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			httpx.NewMockResponse(200, nil, []byte(`{"data": [{"index": 0, "embedding": [1]}, {"index": 2, "embedding": [2]}]}`)),
			httpx.NewMockResponse(200, nil, []byte(`{"data": [{"index": 1, "embedding": [1]}, {"index": 1, "embedding": [2]}]}`)),
		},
	})
	svc := testService(mocks)

	texts := []string{"To reset your password...", "Our refund policy..."}

	_, err := svc.EmbedPassages(ctx, texts)
	assert.EqualError(t, err, `embeddings response contains out of range index 2`)

	// a repeated index would otherwise leave an input with a nil embedding
	_, err = svc.EmbedPassages(ctx, texts)
	assert.EqualError(t, err, `embeddings response contains duplicate index 1`)

	assert.False(t, mocks.HasUnused())
}

func TestEmbedPassagesBatching(t *testing.T) {
	ctx := context.Background()

	// 70 passages should be sent as 3 requests of 32 + 32 + 6 inputs
	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			httpx.NewMockResponse(200, nil, embeddingsBody(32, 0)),
			httpx.NewMockResponse(200, nil, embeddingsBody(32, 32)),
			httpx.NewMockResponse(200, nil, embeddingsBody(6, 64)),
		},
	})
	svc := testService(mocks)

	texts := make([]string, 70)
	for i := range texts {
		texts[i] = fmt.Sprintf("passage number %d", i)
	}

	es, err := svc.EmbedPassages(ctx, texts)
	assert.NoError(t, err)
	require.Len(t, es, 70)
	assert.Equal(t, []float32{0}, es[0])
	assert.Equal(t, []float32{33}, es[33])
	assert.Equal(t, []float32{69}, es[69])

	// check the batch sizes and that inputs stayed in order across batches
	require.Len(t, mocks.Requests(), 3)
	sizes := make([]int, 3)
	for i, req := range mocks.Requests() {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		payload := &struct {
			Input []string `json:"input"`
		}{}
		jsonx.MustUnmarshal(body, payload)
		sizes[i] = len(payload.Input)
	}
	assert.Equal(t, []int{32, 32, 6}, sizes)

	assert.False(t, mocks.HasUnused())
}

func TestTrailingSlashInEndpoint(t *testing.T) {
	ctx := context.Background()

	mocks := httpx.WithMocks(nil, map[string][]*httpx.MockResponse{
		"http://embeddings:8095/v1/embeddings": {
			httpx.NewMockResponse(200, nil, []byte(`{"data": [{"index": 0, "embedding": [1]}]}`)),
		},
	})
	svc := embeddings.NewService(&http.Client{Transport: mocks}, "http://embeddings:8095/v1/", testModel)

	_, err := svc.EmbedQuery(ctx, "hello")
	assert.NoError(t, err)
	assert.False(t, mocks.HasUnused())
}
