package memtest

import (
	"hash/fnv"
	"math"

	"github.com/alamparelli/alf/internal/memory"
)

// StubEmbedder is a deterministic, fast embedder for unit tests. It maps
// text to a small vector via hashed-bag-of-words so semantically similar
// strings (sharing more tokens) yield closer cosine distances, without
// bringing in ONNX or an HTTP dependency.
//
// The output is NOT a semantically meaningful embedding — it only preserves
// enough structure to exercise Store.Index / Store.Search contracts end to
// end. Production code MUST NOT import this.
type StubEmbedder struct {
	Dim   int
	Ready bool // when false, IsReady() returns false and the Store falls back to LIKE
}

// NewStubEmbedder returns a ready-to-use embedder with the given dimension.
// 16 is plenty for unit tests; production defaults to 384.
func NewStubEmbedder(dim int) *StubEmbedder {
	return &StubEmbedder{Dim: dim, Ready: true}
}

func (e *StubEmbedder) Dims() int    { return e.Dim }
func (e *StubEmbedder) IsReady() bool { return e.Ready }

func (e *StubEmbedder) Embed(text string) ([]float32, error) {
	return e.encode(text), nil
}

func (e *StubEmbedder) EmbedQuery(text string) ([]float32, error) {
	return e.encode(text), nil
}

func (e *StubEmbedder) EmbedBatch(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = e.encode(t)
	}
	return out, nil
}

// encode splits on whitespace, hashes each token into a bin, counts, then
// L2-normalises the bin vector. Cosine similarity on the result rewards
// shared tokens — good enough to verify the vec path.
func (e *StubEmbedder) encode(text string) []float32 {
	v := make([]float32, e.Dim)
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == ' ' || text[i] == '\t' || text[i] == '\n' {
			if i > start {
				h := fnv.New32a()
				_, _ = h.Write([]byte(text[start:i]))
				v[int(h.Sum32())%e.Dim] += 1
			}
			start = i + 1
		}
	}
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if norm == 0 {
		// Keep a non-zero vector so cosine distance stays defined (some vec
		// backends return NaN for zero-length embeddings).
		v[0] = 1
		return v
	}
	n := float32(math.Sqrt(norm))
	for i := range v {
		v[i] /= n
	}
	return v
}

// compile-time check that StubEmbedder satisfies the interface.
var _ memory.Embedder = (*StubEmbedder)(nil)
