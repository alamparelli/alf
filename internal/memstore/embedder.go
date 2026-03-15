package memstore

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Embedder manages ONNX Runtime inference for semantic embeddings.
// The model is loaded once and kept in memory for fast embedding generation.
// For E5 models, text is prefixed with "passage: " (storage) or "query: " (search).
type Embedder struct {
	mu            sync.Mutex
	modelDir      string
	dims          int
	maxLen        int
	queryPrefix   string
	passagePrefix string
	tokenizer     *Tokenizer
	session       *ort.DynamicAdvancedSession
	ready         bool
}

// NewEmbedder creates a new Embedder. Call Start() to load the model.
func NewEmbedder(modelDir string) (*Embedder, error) {
	if modelDir == "" {
		modelDir = "/opt/alf/models/multilingual-e5-small"
	}

	modelPath := filepath.Join(modelDir, "model.onnx")
	tokenizerPath := filepath.Join(modelDir, "tokenizer.json")

	for _, p := range []string{modelPath, tokenizerPath} {
		if _, err := os.Stat(p); err != nil {
			return nil, fmt.Errorf("missing model file: %s", p)
		}
	}

	return &Embedder{
		modelDir:      modelDir,
		maxLen:        512,
		queryPrefix:   "query: ",
		passagePrefix: "passage: ",
	}, nil
}

// Start initialises ONNX Runtime, loads the model, and runs a warmup inference.
func (e *Embedder) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ready {
		return nil
	}

	// Load tokenizer.
	tokPath := filepath.Join(e.modelDir, "tokenizer.json")
	tok, err := NewTokenizer(tokPath, e.maxLen)
	if err != nil {
		return fmt.Errorf("load tokenizer: %w", err)
	}
	e.tokenizer = tok

	// Initialise ONNX Runtime (idempotent across the process).
	libPath := os.Getenv("ONNXRUNTIME_LIB")
	if libPath == "" {
		libPath = "/usr/local/lib/libonnxruntime.so"
	}
	ort.SetSharedLibraryPath(libPath)
	if err := ort.InitializeEnvironment(); err != nil {
		// Already initialised is OK.
		if err.Error() != "The ONNX runtime has already been initialized" {
			return fmt.Errorf("init onnxruntime: %w", err)
		}
	}

	// Create session.
	modelPath := filepath.Join(e.modelDir, "model.onnx")
	session, err := ort.NewDynamicAdvancedSession(
		modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	e.session = session

	// Warmup to infer dims.
	dims, err := e.warmup()
	if err != nil {
		session.Destroy()
		return fmt.Errorf("warmup: %w", err)
	}
	e.dims = dims
	e.ready = true

	log.Printf("memstore: embedder ready (model-dir=%s, dims=%d)", e.modelDir, e.dims)
	return nil
}

// warmup runs a single dummy inference to determine output dimensions.
func (e *Embedder) warmup() (int, error) {
	ids, mask, types := e.tokenizer.Encode("hello")

	inputIDs, err := ort.NewTensor(ort.NewShape(1, int64(e.maxLen)), ids)
	if err != nil {
		return 0, err
	}
	defer inputIDs.Destroy()

	attentionMask, err := ort.NewTensor(ort.NewShape(1, int64(e.maxLen)), mask)
	if err != nil {
		return 0, err
	}
	defer attentionMask.Destroy()

	tokenTypeIDs, err := ort.NewTensor(ort.NewShape(1, int64(e.maxLen)), types)
	if err != nil {
		return 0, err
	}
	defer tokenTypeIDs.Destroy()

	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(e.maxLen), 384)) // both MiniLM and E5-small output 384 dims
	if err != nil {
		return 0, err
	}
	defer output.Destroy()

	err = e.session.Run(
		[]ort.Value{inputIDs, attentionMask, tokenTypeIDs},
		[]ort.Value{output},
	)
	if err != nil {
		return 0, err
	}

	shape := output.GetShape()
	if len(shape) < 3 {
		return 0, fmt.Errorf("unexpected output shape: %v", shape)
	}
	return int(shape[2]), nil
}

// Stop releases all ONNX resources.
func (e *Embedder) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.session != nil {
		e.session.Destroy()
		e.session = nil
		e.ready = false
		log.Println("memstore: embedder stopped")
	}
}

// IsReady returns true if the model is loaded and ready.
func (e *Embedder) IsReady() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ready
}

// Dims returns the embedding dimension count.
func (e *Embedder) Dims() int {
	return e.dims
}

// Embed generates an embedding for a single text, prefixed with "passage: " for E5 models.
// Use this for storing/indexing content.
func (e *Embedder) Embed(text string) ([]float32, error) {
	vecs, err := e.EmbedBatch([]string{e.passagePrefix + text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedQuery generates an embedding for a search query, prefixed with "query: " for E5 models.
// Use this for search/retrieval operations.
func (e *Embedder) EmbedQuery(text string) ([]float32, error) {
	vecs, err := e.EmbedBatch([]string{e.queryPrefix + text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedBatch generates embeddings for multiple texts.
func (e *Embedder) EmbedBatch(texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.ready {
		return nil, fmt.Errorf("embedder not ready")
	}

	batch := int64(len(texts))
	seqLen := int64(e.maxLen)

	// Tokenize all texts into flat slices.
	allIDs, allMask, allTypes := e.tokenizer.EncodeBatch(texts)

	// Create input tensors.
	inputIDs, err := ort.NewTensor(ort.NewShape(batch, seqLen), allIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer inputIDs.Destroy()

	attentionMask, err := ort.NewTensor(ort.NewShape(batch, seqLen), allMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer attentionMask.Destroy()

	tokenTypeIDs, err := ort.NewTensor(ort.NewShape(batch, seqLen), allTypes)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer tokenTypeIDs.Destroy()

	// Create output tensor: (batch, seqLen, dims).
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(batch, seqLen, int64(e.dims)))
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer output.Destroy()

	// Run inference.
	err = e.session.Run(
		[]ort.Value{inputIDs, attentionMask, tokenTypeIDs},
		[]ort.Value{output},
	)
	if err != nil {
		return nil, fmt.Errorf("run inference: %w", err)
	}

	// Mean pooling + L2 normalize.
	raw := output.GetData()
	return meanPoolNormalize(raw, allMask, int(batch), int(seqLen), e.dims), nil
}

// meanPoolNormalize applies attention-masked mean pooling and L2 normalization.
func meanPoolNormalize(embeddings []float32, mask []int64, batch, seqLen, dims int) [][]float32 {
	results := make([][]float32, batch)

	for b := 0; b < batch; b++ {
		pooled := make([]float32, dims)
		var count float32

		for s := 0; s < seqLen; s++ {
			m := float32(mask[b*seqLen+s])
			if m == 0 {
				continue
			}
			count += m
			offset := (b*seqLen + s) * dims
			for d := 0; d < dims; d++ {
				pooled[d] += embeddings[offset+d] * m
			}
		}

		if count < 1e-9 {
			count = 1e-9
		}

		// Mean.
		var norm float64
		for d := 0; d < dims; d++ {
			pooled[d] /= count
			norm += float64(pooled[d]) * float64(pooled[d])
		}

		// L2 normalize.
		norm = math.Sqrt(norm)
		if norm < 1e-12 {
			norm = 1e-12
		}
		for d := 0; d < dims; d++ {
			pooled[d] = float32(float64(pooled[d]) / norm)
		}

		results[b] = pooled
	}

	return results
}

// IsAvailable checks if the ONNX model files exist at the given directory.
func IsAvailable(modelDir string) bool {
	if modelDir == "" {
		modelDir = "/opt/alf/models/multilingual-e5-small"
	}
	for _, name := range []string{"model.onnx", "tokenizer.json"} {
		if _, err := os.Stat(filepath.Join(modelDir, name)); err != nil {
			return false
		}
	}
	return true
}
