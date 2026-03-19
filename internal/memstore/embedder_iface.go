package memstore

// EmbedderI abstracts embedding generation for semantic memory search.
// Implementations: *Embedder (in-process ONNX) and *HTTPEmbedder (remote HTTP service).
type EmbedderI interface {
	Embed(text string) ([]float32, error)
	EmbedQuery(text string) ([]float32, error)
	EmbedBatch(texts []string) ([][]float32, error)
	IsReady() bool
	Dims() int
}

// Compile-time interface compliance.
var _ EmbedderI = (*Embedder)(nil)
var _ EmbedderI = (*HTTPEmbedder)(nil)
