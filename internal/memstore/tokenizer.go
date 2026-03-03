package memstore

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Tokenizer implements a BERT-compatible WordPiece tokenizer.
// It loads vocabulary from a HuggingFace tokenizer.json file.
type Tokenizer struct {
	vocab    map[string]int32
	unkID    int32
	clsID    int32
	sepID    int32
	padID    int32
	maxLen   int
}

// tokenizerJSON matches the HuggingFace tokenizer.json structure.
type tokenizerJSON struct {
	Model struct {
		Vocab map[string]int32 `json:"vocab"`
	} `json:"model"`
}

// NewTokenizer loads a WordPiece tokenizer from a HuggingFace tokenizer.json.
func NewTokenizer(path string, maxLen int) (*Tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer: %w", err)
	}

	var tj tokenizerJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("parse tokenizer: %w", err)
	}

	if len(tj.Model.Vocab) == 0 {
		return nil, fmt.Errorf("empty vocab in %s", path)
	}

	t := &Tokenizer{
		vocab:  tj.Model.Vocab,
		maxLen: maxLen,
	}

	// Resolve special token IDs.
	var ok bool
	if t.padID, ok = t.vocab["[PAD]"]; !ok {
		return nil, fmt.Errorf("missing [PAD] in vocab")
	}
	if t.unkID, ok = t.vocab["[UNK]"]; !ok {
		return nil, fmt.Errorf("missing [UNK] in vocab")
	}
	if t.clsID, ok = t.vocab["[CLS]"]; !ok {
		return nil, fmt.Errorf("missing [CLS] in vocab")
	}
	if t.sepID, ok = t.vocab["[SEP]"]; !ok {
		return nil, fmt.Errorf("missing [SEP] in vocab")
	}

	return t, nil
}

// Encode tokenizes a single text into input_ids, attention_mask, and token_type_ids.
// Output is padded/truncated to maxLen.
func (t *Tokenizer) Encode(text string) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	tokens := t.tokenize(text)

	// Truncate to maxLen-2 to leave room for [CLS] and [SEP].
	if len(tokens) > t.maxLen-2 {
		tokens = tokens[:t.maxLen-2]
	}

	// Build input_ids: [CLS] + tokens + [SEP] + [PAD]...
	ids := make([]int64, t.maxLen)
	mask := make([]int64, t.maxLen)
	types := make([]int64, t.maxLen) // all zeros for single-sentence

	ids[0] = int64(t.clsID)
	mask[0] = 1
	for i, tok := range tokens {
		ids[i+1] = int64(tok)
		mask[i+1] = 1
	}
	ids[len(tokens)+1] = int64(t.sepID)
	mask[len(tokens)+1] = 1

	// Remaining positions are already 0 (padID=0, mask=0, types=0).
	return ids, mask, types
}

// EncodeBatch tokenizes multiple texts. Returns parallel slices.
func (t *Tokenizer) EncodeBatch(texts []string) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	n := len(texts)
	allIDs := make([]int64, 0, n*t.maxLen)
	allMask := make([]int64, 0, n*t.maxLen)
	allTypes := make([]int64, 0, n*t.maxLen)

	for _, text := range texts {
		ids, mask, types := t.Encode(text)
		allIDs = append(allIDs, ids...)
		allMask = append(allMask, mask...)
		allTypes = append(allTypes, types...)
	}

	return allIDs, allMask, allTypes
}

// tokenize applies BERT preprocessing: lowercase → whitespace split → WordPiece.
func (t *Tokenizer) tokenize(text string) []int32 {
	// Lowercase and normalize whitespace.
	text = strings.ToLower(text)

	// Split on whitespace and punctuation (BERT basic tokenization).
	words := tokenizeBasic(text)

	var ids []int32
	for _, word := range words {
		ids = append(ids, t.wordPiece(word)...)
	}
	return ids
}

// wordPiece splits a single word into WordPiece subword tokens.
func (t *Tokenizer) wordPiece(word string) []int32 {
	if _, ok := t.vocab[word]; ok {
		return []int32{t.vocab[word]}
	}

	var ids []int32
	start := 0
	runes := []rune(word)

	for start < len(runes) {
		end := len(runes)
		found := false

		for end > start {
			substr := string(runes[start:end])
			if start > 0 {
				substr = "##" + substr
			}

			if id, ok := t.vocab[substr]; ok {
				ids = append(ids, id)
				start = end
				found = true
				break
			}
			end--
		}

		if !found {
			ids = append(ids, t.unkID)
			start++
		}
	}

	return ids
}

// tokenizeBasic splits text on whitespace and punctuation boundaries,
// matching BERT's BasicTokenizer behavior.
func tokenizeBasic(text string) []string {
	var tokens []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsSpace(r) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}

		if isPunct(r) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(r))
			continue
		}

		current.WriteRune(r)
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// isPunct returns true if the rune is a punctuation character (BERT definition).
func isPunct(r rune) bool {
	if (r >= 33 && r <= 47) || (r >= 58 && r <= 64) ||
		(r >= 91 && r <= 96) || (r >= 123 && r <= 126) {
		return true
	}
	return unicode.IsPunct(r)
}
