// Package knowledge implements the knowledge base feature - chunking and embedding of source content for
// indexing, and semantic search over the resulting chunks.
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/nyaruka/gocommon/jsonx"
	"github.com/nyaruka/mailroom/v26/core/models"
	"github.com/nyaruka/mailroom/v26/runtime"
)

// the e5 family of embedding models requires inputs to be prefixed according to their use
const (
	passagePrefix = "passage: " // for content being indexed
	queryPrefix   = "query: "   // for search queries
)

// requestBatchSize is the maximum number of inputs sent to the embeddings service in a single request. In
// production the service sits behind a shared load balancer with latency alarms, so we prefer more smaller
// requests over fewer large slow ones.
const requestBatchSize = 32

type embeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingsResponse struct {
	Data []struct {
		Index     int              `json:"index"`
		Embedding models.Embedding `json:"embedding"`
	} `json:"data"`
}

// EmbedPassages fetches embeddings for the given passages of source content being indexed, returned in the
// same order. Inputs are sent in batches of at most requestBatchSize.
func EmbedPassages(ctx context.Context, rt *runtime.Runtime, texts []string) ([]models.Embedding, error) {
	return embed(ctx, rt, passagePrefix, texts)
}

// EmbedQuery fetches the embedding for a search query.
func EmbedQuery(ctx context.Context, rt *runtime.Runtime, text string) (models.Embedding, error) {
	es, err := embed(ctx, rt, queryPrefix, []string{text})
	if err != nil {
		return nil, err
	}
	return es[0], nil
}

func embed(ctx context.Context, rt *runtime.Runtime, prefix string, texts []string) ([]models.Embedding, error) {
	if rt.Config.EmbeddingsEndpoint == "" {
		return nil, errors.New("no embeddings service configured")
	}

	inputs := make([]string, len(texts))
	for i := range texts {
		inputs[i] = prefix + texts[i]
	}

	embeddings := make([]models.Embedding, 0, len(inputs))
	for batch := range slices.Chunk(inputs, requestBatchSize) {
		batchEmbeddings, err := request(ctx, rt, batch)
		if err != nil {
			return nil, err
		}
		embeddings = append(embeddings, batchEmbeddings...)
	}
	return embeddings, nil
}

// makes a single request to the embeddings service, returning the embeddings in input order
func request(ctx context.Context, rt *runtime.Runtime, inputs []string) ([]models.Embedding, error) {
	payload := jsonx.MustMarshal(&embeddingsRequest{Model: rt.Config.EmbeddingsModel, Input: inputs})

	req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(rt.Config.EmbeddingsEndpoint, "/")+"/embeddings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	// this is an internal request to a fixed service whose trace we don't persist, so a plain fetch is enough
	resp, err := rt.HTTP.Services.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error calling embeddings endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading embeddings response: %w", err)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("error calling embeddings endpoint, got non-200 status: %s", string(body))
	}

	er := &embeddingsResponse{}
	if err := json.Unmarshal(body, er); err != nil {
		return nil, fmt.Errorf("error unmarshaling embeddings response: %w", err)
	}
	if len(er.Data) != len(inputs) {
		return nil, fmt.Errorf("embeddings response contains %d embeddings for %d inputs", len(er.Data), len(inputs))
	}

	embeddings := make([]models.Embedding, len(inputs))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(inputs) {
			return nil, fmt.Errorf("embeddings response contains out of range index %d", d.Index)
		}
		embeddings[d.Index] = d.Embedding
	}
	return embeddings, nil
}
