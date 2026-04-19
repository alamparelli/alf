package memstore

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestNewEmbedder_MissingModelFile(t *testing.T) {
	dir := t.TempDir()
	// Only tokenizer.json present; model.onnx missing.
	os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte("{}"), 0o644)

	_, err := NewEmbedder(dir)
	if err == nil {
		t.Fatal("expected error when model.onnx is missing")
	}
}

func TestNewEmbedder_MissingTokenizer(t *testing.T) {
	dir := t.TempDir()
	// Only model.onnx present; tokenizer.json missing.
	os.WriteFile(filepath.Join(dir, "model.onnx"), []byte("dummy"), 0o644)

	_, err := NewEmbedder(dir)
	if err == nil {
		t.Fatal("expected error when tokenizer.json is missing")
	}
}

// NewEmbedder with both files present must succeed (lazy: no ONNX init
// happens until Start() is called).
func TestNewEmbedder_FilesPresent(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "model.onnx"), []byte("dummy"), 0o644)
	os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte("{}"), 0o644)

	e, err := NewEmbedder(dir)
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if e.modelDir != dir {
		t.Errorf("unexpected modelDir: %s", e.modelDir)
	}
	if e.passagePrefix != "passage: " || e.queryPrefix != "query: " {
		t.Errorf("unexpected E5 prefixes: %q / %q", e.passagePrefix, e.queryPrefix)
	}
}

func TestEmbedder_IsReady_Dims_Stop_NoInit(t *testing.T) {
	// A zero-value embedder simulates a stopped/uninitialised state.
	e := &Embedder{dims: 384}
	if e.IsReady() {
		t.Error("expected IsReady=false on fresh embedder")
	}
	if got := e.Dims(); got != 384 {
		t.Errorf("expected Dims=384, got %d", got)
	}
	// Stop() on an embedder with no session must be a no-op (not panic).
	e.Stop()
}

func TestEmbedder_EmbedNotReady(t *testing.T) {
	e := &Embedder{passagePrefix: "passage: ", queryPrefix: "query: ", maxLen: 512}
	if _, err := e.Embed("hello"); err == nil {
		t.Error("expected error when embedder not ready")
	}
	if _, err := e.EmbedQuery("hello"); err == nil {
		t.Error("expected error when embedder not ready")
	}
	if _, err := e.EmbedBatch([]string{"a"}); err == nil {
		t.Error("expected error when embedder not ready")
	}
}

func TestIsAvailable(t *testing.T) {
	dir := t.TempDir()
	// Empty dir → not available.
	if IsAvailable(dir) {
		t.Error("expected IsAvailable=false on empty dir")
	}

	os.WriteFile(filepath.Join(dir, "model.onnx"), []byte(""), 0o644)
	if IsAvailable(dir) {
		t.Error("expected IsAvailable=false when tokenizer.json missing")
	}

	os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte(""), 0o644)
	if !IsAvailable(dir) {
		t.Error("expected IsAvailable=true when both files present")
	}
}

// IsAvailable with empty modelDir falls back to the baked-in default; the
// default path is expected to NOT exist on the test host.
func TestIsAvailable_EmptyDirFallback(t *testing.T) {
	// We cannot assume anything about /opt/alf/models on the test host,
	// but we can assert that IsAvailable does not panic and returns a bool.
	_ = IsAvailable("")
}

func TestMeanPoolNormalize_MaskedTokensExcluded(t *testing.T) {
	// Batch=1, seqLen=2, dims=2. Token 0 is real (mask=1), token 1 is padding (mask=0).
	// Only token 0 contributes. Vector = [3, 4]; L2-norm = 5 → normalized = [0.6, 0.8].
	emb := []float32{3, 4, 99, 99}
	mask := []int64{1, 0}
	out := meanPoolNormalize(emb, mask, 1, 2, 2)

	if len(out) != 1 || len(out[0]) != 2 {
		t.Fatalf("unexpected output shape: %+v", out)
	}
	if math.Abs(float64(out[0][0])-0.6) > 1e-5 {
		t.Errorf("dim 0: expected 0.6, got %f", out[0][0])
	}
	if math.Abs(float64(out[0][1])-0.8) > 1e-5 {
		t.Errorf("dim 1: expected 0.8, got %f", out[0][1])
	}
}

func TestMeanPoolNormalize_AllMasked(t *testing.T) {
	// All tokens padded → count≈0, pooled stays 0 after division by epsilon,
	// L2-norm≈0 → divided by 1e-12; result must not contain NaN.
	emb := []float32{0.5, 0.5}
	mask := []int64{0}
	out := meanPoolNormalize(emb, mask, 1, 1, 2)
	if len(out) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(out))
	}
	for _, v := range out[0] {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Errorf("all-masked pool produced NaN/Inf: %v", out[0])
		}
	}
}

func TestMeanPoolNormalize_BatchIsolation(t *testing.T) {
	// Batch=2, seqLen=1, dims=2. Each batch should normalize independently.
	// Batch 0 = [1, 0] → norm 1 → [1, 0]. Batch 1 = [0, 2] → norm 2 → [0, 1].
	emb := []float32{1, 0, 0, 2}
	mask := []int64{1, 1}
	out := meanPoolNormalize(emb, mask, 2, 1, 2)
	if len(out) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(out))
	}
	if out[0][0] != 1 || out[0][1] != 0 {
		t.Errorf("batch 0 wrong: %v", out[0])
	}
	if out[1][0] != 0 || out[1][1] != 1 {
		t.Errorf("batch 1 wrong: %v", out[1])
	}
}
