package testsuite

import (
	"context"
	"hash/fnv"
)

// embeddingDimensions matches the vector(384) column the chunks are stored in, so mock embeddings can be written
// to the database like real ones
const embeddingDimensions = 384

// MockEmbedding creates an embedding of the right dimensions with its leading components set from vals
func MockEmbedding(vals ...float32) []float32 {
	e := make([]float32, embeddingDimensions)
	copy(e, vals)
	return e
}

// MockEmbedder is a runtime.Embedder for tests, so that tests of things which embed don't have to mock out an
// HTTP service - the real client is tested against mocked HTTP in its own package. A text with no configured
// vector gets a deterministic one derived from it, which is enough for tests that only care that indexing
// happened; tests that assert on search ordering configure the vectors they need.
type MockEmbedder struct {
	Vectors map[string][]float32 // vectors to return for particular texts
	Error   error                // if set, every call fails with this

	Passages []string // the passages embedded so far
	Queries  []string // the queries embedded so far
}

func (e *MockEmbedder) EmbedPassages(ctx context.Context, texts []string) ([][]float32, error) {
	if e.Error != nil {
		return nil, e.Error
	}

	embeddings := make([][]float32, len(texts))
	for i, text := range texts {
		e.Passages = append(e.Passages, text)
		embeddings[i] = e.vector(text)
	}
	return embeddings, nil
}

func (e *MockEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if e.Error != nil {
		return nil, e.Error
	}

	e.Queries = append(e.Queries, text)
	return e.vector(text), nil
}

// returns the configured vector for the given text, or a unit vector in a dimension derived from it
func (e *MockEmbedder) vector(text string) []float32 {
	if v, ok := e.Vectors[text]; ok {
		return v
	}

	h := fnv.New32a()
	h.Write([]byte(text))

	v := make([]float32, embeddingDimensions)
	v[h.Sum32()%embeddingDimensions] = 1
	return v
}
