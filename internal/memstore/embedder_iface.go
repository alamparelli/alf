package memstore

import (
	"time"

	"github.com/alamparelli/alf/internal/memory"
)

// EmbedderI is kept as an alias of memory.Embedder during the Step 1.3
// transition (#337). Existing memstore code and callers continue to
// compile unchanged; new code should reference memory.Embedder directly.
// This alias will be removed once memstore itself is retired.
type EmbedderI = memory.Embedder

// HTTPEmbedder moved to the memory package in #337 — kept as an alias so
// in-flight callers compile without churn. Prefer memory.HTTPEmbedder in
// new code.
type HTTPEmbedder = memory.HTTPEmbedder

// NewHTTPEmbedder is re-exported for the same reason. Direct call site:
// memory.NewHTTPEmbedder.
func NewHTTPEmbedder(baseURL, instanceID, secret string, timeout time.Duration) *HTTPEmbedder {
	return memory.NewHTTPEmbedder(baseURL, instanceID, secret, timeout)
}

// Compile-time interface compliance for the in-process ONNX embedder.
var _ EmbedderI = (*Embedder)(nil)
