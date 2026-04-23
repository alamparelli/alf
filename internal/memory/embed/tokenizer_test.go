package embed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeTestTokenizer creates a temporary tokenizer.json file.
func writeTestTokenizer(t *testing.T, content any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tokenizer.json")
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTokenizerWordPiece(t *testing.T) {
	// Minimal WordPiece vocab (BERT-style).
	vocab := map[string]int32{
		"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
		"hello": 4, "world": 5, "##s": 6,
	}
	path := writeTestTokenizer(t, map[string]any{
		"model": map[string]any{
			"type":  "WordPiece",
			"vocab": vocab,
		},
	})

	tok, err := NewTokenizer(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	if tok.mode != "wordpiece" {
		t.Fatalf("expected wordpiece mode, got %s", tok.mode)
	}

	ids, mask, types := tok.Encode("hello world")
	// Expected: [CLS]=2, hello=4, world=5, [SEP]=3, pad...
	if ids[0] != 2 {
		t.Errorf("expected CLS=2, got %d", ids[0])
	}
	if ids[1] != 4 {
		t.Errorf("expected hello=4, got %d", ids[1])
	}
	if ids[2] != 5 {
		t.Errorf("expected world=5, got %d", ids[2])
	}
	if ids[3] != 3 {
		t.Errorf("expected SEP=3, got %d", ids[3])
	}
	if mask[0] != 1 || mask[3] != 1 || mask[4] != 0 {
		t.Error("attention mask wrong")
	}
	if types[0] != 0 {
		t.Error("token_type_ids should be all zeros")
	}
}

func TestTokenizerUnigram(t *testing.T) {
	// Minimal Unigram/SentencePiece vocab (XLM-R style).
	vocabList := [][]any{
		{"<s>", 0.0},
		{"<pad>", 0.0},
		{"</s>", 0.0},
		{"<unk>", 0.0},
		{"▁hello", -1.0},
		{"▁world", -1.0},
		{"▁", -2.0},
		{"h", -3.0},
		{"e", -3.0},
		{"l", -3.0},
		{"o", -3.0},
	}
	path := writeTestTokenizer(t, map[string]any{
		"model": map[string]any{
			"type":  "Unigram",
			"vocab": vocabList,
		},
	})

	tok, err := NewTokenizer(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	if tok.mode != "unigram" {
		t.Fatalf("expected unigram mode, got %s", tok.mode)
	}

	ids, mask, _ := tok.Encode("hello world")
	// CLS token should be <s> = 0
	if ids[0] != 0 {
		t.Errorf("expected CLS=0 (<s>), got %d", ids[0])
	}
	// Should have tokens between CLS and SEP
	seqLen := 0
	for i := range mask {
		if mask[i] == 1 {
			seqLen++
		}
	}
	if seqLen < 3 {
		t.Errorf("expected at least 3 tokens (CLS + content + SEP), got %d", seqLen)
	}
	// Last non-pad token should be SEP = </s> = 2
	if ids[seqLen-1] != 2 {
		t.Errorf("expected SEP=2 (</s>), got %d", ids[seqLen-1])
	}
}

func TestTokenizerUnigramWithAddedTokens(t *testing.T) {
	vocabList := [][]any{
		{"<s>", 0.0},
		{"<pad>", 0.0},
		{"</s>", 0.0},
		{"<unk>", 0.0},
		{"▁test", -1.0},
	}
	path := writeTestTokenizer(t, map[string]any{
		"model": map[string]any{
			"type":  "Unigram",
			"vocab": vocabList,
		},
		"added_tokens": []map[string]any{
			{"id": 5, "content": "<mask>", "special": true},
		},
	})

	tok, err := NewTokenizer(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	// <mask> should be in vocab
	if _, ok := tok.vocab["<mask>"]; !ok {
		t.Error("expected <mask> in vocab from added_tokens")
	}
}

func TestTokenizerEmptyVocab(t *testing.T) {
	path := writeTestTokenizer(t, map[string]any{
		"model": map[string]any{
			"vocab": map[string]int32{},
		},
	})

	_, err := NewTokenizer(path, 16)
	if err == nil {
		t.Fatal("expected error for empty vocab")
	}
}

func TestTokenizerEncodeBatch(t *testing.T) {
	vocab := map[string]int32{
		"[PAD]": 0, "[UNK]": 1, "[CLS]": 2, "[SEP]": 3,
		"hello": 4, "world": 5,
	}
	path := writeTestTokenizer(t, map[string]any{
		"model": map[string]any{"vocab": vocab},
	})

	tok, err := NewTokenizer(path, 8)
	if err != nil {
		t.Fatal(err)
	}

	ids, mask, types := tok.EncodeBatch([]string{"hello", "world"})
	if len(ids) != 16 {
		t.Errorf("expected 16 ids (2 * 8), got %d", len(ids))
	}
	if len(mask) != 16 {
		t.Errorf("expected 16 mask values, got %d", len(mask))
	}
	if len(types) != 16 {
		t.Errorf("expected 16 type values, got %d", len(types))
	}
}
