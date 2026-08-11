// Package embeddings implements a client for an OpenAI compatible embeddings service, used to turn text into
// the vectors that knowledge base indexing and search are built on.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/nyaruka/gocommon/jsonx"
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
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Service is a client for an OpenAI compatible embeddings service
type Service struct {
	httpClient *http.Client
	endpoint   string
	model      string
}

// NewService creates a new embeddings service client for the given endpoint and model
func NewService(httpClient *http.Client, endpoint, model string) *Service {
	return &Service{httpClient: httpClient, endpoint: strings.TrimRight(endpoint, "/"), model: model}
}

// EmbedPassages fetches embeddings for the given passages of source content being indexed, returned in the
// same order. Inputs are sent in batches of at most requestBatchSize.
func (s *Service) EmbedPassages(ctx context.Context, texts []string) ([][]float32, error) {
	return s.embed(ctx, passagePrefix, texts)
}

// EmbedQuery fetches the embedding for a search query.
func (s *Service) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	es, err := s.embed(ctx, queryPrefix, []string{text})
	if err != nil {
		return nil, err
	}
	return es[0], nil
}

func (s *Service) embed(ctx context.Context, prefix string, texts []string) ([][]float32, error) {
	inputs := make([]string, len(texts))
	for i := range texts {
		inputs[i] = prefix + texts[i]
	}

	embeddings := make([][]float32, 0, len(inputs))
	for batch := range slices.Chunk(inputs, requestBatchSize) {
		batchEmbeddings, err := s.request(ctx, batch)
		if err != nil {
			return nil, err
		}
		embeddings = append(embeddings, batchEmbeddings...)
	}
	return embeddings, nil
}

// makes a single request to the embeddings service, returning the embeddings in input order
func (s *Service) request(ctx context.Context, inputs []string) ([][]float32, error) {
	payload := jsonx.MustMarshal(&embeddingsRequest{Model: s.model, Input: inputs})

	req, _ := http.NewRequestWithContext(ctx, "POST", s.endpoint+"/embeddings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	// this is an internal request to a fixed service whose trace we don't persist, so a plain fetch is enough
	resp, err := s.httpClient.Do(req)
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

	// with the count checked above, rejecting out of range and repeated indexes is what guarantees every input
	// got an embedding - otherwise a repeat would leave another input silently holding a nil one
	embeddings := make([][]float32, len(inputs))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(inputs) {
			return nil, fmt.Errorf("embeddings response contains out of range index %d", d.Index)
		}
		if embeddings[d.Index] != nil {
			return nil, fmt.Errorf("embeddings response contains duplicate index %d", d.Index)
		}
		embeddings[d.Index] = d.Embedding
	}
	return embeddings, nil
}
