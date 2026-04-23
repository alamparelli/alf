package memory

// Embedder generates vector representations of text for semantic search.
//
// Two implementations exist (ported from memstore in #337): an in-process
// ONNX-backed embedder for offline/isolated runs, and an HTTP client that
// talks to a dedicated embed-server subprocess. Both satisfy this contract
// and are interchangeable from the Store's perspective.
//
// Dims MUST stay stable across the lifetime of one Store — the vec0 virtual
// table is created with a fixed dimension at schema time. Swapping embedders
// with different Dims() requires rebuilding the vec index.
type Embedder interface {
	// Embed returns a document embedding for text.
	Embed(text string) ([]float32, error)

	// EmbedQuery returns a query embedding. Some model families (e.g.
	// instruction-tuned retrievers) prefix document vs query differently, so
	// callers MUST use EmbedQuery at Search time and Embed at Index time.
	EmbedQuery(text string) ([]float32, error)

	// EmbedBatch returns embeddings for a batch of documents. Order must
	// match the input slice.
	EmbedBatch(texts []string) ([][]float32, error)

	// IsReady reports whether the embedder can serve requests. The Store
	// gates vec writes on this — a not-ready embedder falls back to the
	// non-vector Index/Search path.
	IsReady() bool

	// Dims returns the embedding dimension. MUST be constant for the
	// lifetime of the value.
	Dims() int
}
