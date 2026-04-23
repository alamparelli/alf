package embed

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// Tokenizer implements a multi-format HuggingFace tokenizer.
// Supports both WordPiece (BERT/MiniLM) and Unigram/SentencePiece (XLM-R/E5) models.
type Tokenizer struct {
	vocab  map[string]int32
	scores map[string]float64 // Unigram scores (nil for WordPiece)
	unkID  int32
	clsID  int32
	sepID  int32
	padID  int32
	maxLen int
	mode   string // "wordpiece" or "unigram"
}

// tokenizerJSON matches the HuggingFace tokenizer.json structure.
// The vocab field can be either map[string]int32 (WordPiece) or [][]any (Unigram).
type tokenizerJSON struct {
	Model struct {
		Type  string          `json:"type"`
		Vocab json.RawMessage `json:"vocab"`
	} `json:"model"`
	AddedTokens []struct {
		ID      int32  `json:"id"`
		Content string `json:"content"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
}

// NewTokenizer loads a tokenizer from a HuggingFace tokenizer.json.
// Auto-detects WordPiece vs Unigram format.
func NewTokenizer(path string, maxLen int) (*Tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokenizer: %w", err)
	}

	var tj tokenizerJSON
	if err := json.Unmarshal(data, &tj); err != nil {
		return nil, fmt.Errorf("parse tokenizer: %w", err)
	}

	t := &Tokenizer{maxLen: maxLen}

	// Try WordPiece first (vocab is map[string]int32).
	var vocabMap map[string]int32
	if err := json.Unmarshal(tj.Model.Vocab, &vocabMap); err == nil && len(vocabMap) > 0 {
		t.vocab = vocabMap
		t.mode = "wordpiece"
	} else {
		// Try Unigram (vocab is array of [token, score] pairs).
		var vocabList [][]json.RawMessage
		if err := json.Unmarshal(tj.Model.Vocab, &vocabList); err != nil {
			return nil, fmt.Errorf("parse vocab: unsupported format in %s", path)
		}
		t.vocab = make(map[string]int32, len(vocabList))
		t.scores = make(map[string]float64, len(vocabList))
		for i, entry := range vocabList {
			if len(entry) < 2 {
				continue
			}
			var token string
			var score float64
			if err := json.Unmarshal(entry[0], &token); err != nil {
				continue
			}
			if err := json.Unmarshal(entry[1], &score); err != nil {
				continue
			}
			t.vocab[token] = int32(i)
			t.scores[token] = score
		}
		t.mode = "unigram"
	}

	if len(t.vocab) == 0 {
		return nil, fmt.Errorf("empty vocab in %s", path)
	}

	// Register added_tokens (special tokens like <mask>) into vocab.
	for _, at := range tj.AddedTokens {
		if _, exists := t.vocab[at.Content]; !exists {
			t.vocab[at.Content] = at.ID
		}
	}

	// Resolve special token IDs based on model type.
	if t.mode == "wordpiece" {
		if err := t.resolveSpecial("[PAD]", "[UNK]", "[CLS]", "[SEP]"); err != nil {
			return nil, err
		}
	} else {
		// XLM-RoBERTa / SentencePiece style
		if err := t.resolveSpecial("<pad>", "<unk>", "<s>", "</s>"); err != nil {
			return nil, err
		}
	}

	return t, nil
}

func (t *Tokenizer) resolveSpecial(pad, unk, cls, sep string) error {
	var ok bool
	if t.padID, ok = t.vocab[pad]; !ok {
		return fmt.Errorf("missing %s in vocab", pad)
	}
	if t.unkID, ok = t.vocab[unk]; !ok {
		return fmt.Errorf("missing %s in vocab", unk)
	}
	if t.clsID, ok = t.vocab[cls]; !ok {
		return fmt.Errorf("missing %s in vocab", cls)
	}
	if t.sepID, ok = t.vocab[sep]; !ok {
		return fmt.Errorf("missing %s in vocab", sep)
	}
	return nil
}

// Encode tokenizes a single text into input_ids, attention_mask, and token_type_ids.
// Output is padded/truncated to maxLen.
func (t *Tokenizer) Encode(text string) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	var tokens []int32
	if t.mode == "unigram" {
		tokens = t.tokenizeUnigram(text)
	} else {
		tokens = t.tokenizeWordPiece(text)
	}

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

// ========== WordPiece tokenization (BERT/MiniLM) ==========

func (t *Tokenizer) tokenizeWordPiece(text string) []int32 {
	text = strings.ToLower(text)
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

// ========== Unigram tokenization (SentencePiece/XLM-R/E5) ==========

func (t *Tokenizer) tokenizeUnigram(text string) []int32 {
	// SentencePiece Metaspace pre-tokenizer: replace spaces with ▁ (U+2581)
	// and add ▁ at the start.
	text = strings.ToLower(text)
	text = "▁" + strings.ReplaceAll(text, " ", "▁")

	// Viterbi-based Unigram tokenization.
	pieces := t.unigramSegment(text)

	ids := make([]int32, 0, len(pieces))
	for _, p := range pieces {
		if id, ok := t.vocab[p]; ok {
			ids = append(ids, id)
		} else {
			ids = append(ids, t.unkID)
		}
	}
	return ids
}

// unigramSegment implements a Viterbi-style best-path segmentation
// using the Unigram LM scores. This matches the SentencePiece algorithm.
func (t *Tokenizer) unigramSegment(text string) []string {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return nil
	}

	const negInf = -1e18

	// best[i] = best log-prob to tokenize runes[0:i]
	best := make([]float64, n+1)
	// backtrack[i] = start position of the best token ending at position i
	backtrack := make([]int, n+1)

	for i := range best {
		best[i] = negInf
	}
	best[0] = 0

	for i := 0; i < n; i++ {
		if best[i] == negInf {
			continue
		}
		// Try all substrings starting at i.
		maxEnd := i + 32 // limit substring length for performance
		if maxEnd > n {
			maxEnd = n
		}
		for j := i + 1; j <= maxEnd; j++ {
			substr := string(runes[i:j])
			if score, ok := t.scores[substr]; ok {
				candidate := best[i] + score
				if candidate > best[j] {
					best[j] = candidate
					backtrack[j] = i
				}
			}
		}
		// Fallback: single character (as UNK) if no token starts at i+1
		if i+1 <= n && best[i+1] == negInf {
			// Use a very low score for single-char fallback
			best[i+1] = best[i] + negInf/float64(n)
			backtrack[i+1] = i
		}
	}

	// Backtrack to recover the segmentation.
	var pieces []string
	pos := n
	for pos > 0 {
		start := backtrack[pos]
		pieces = append(pieces, string(runes[start:pos]))
		pos = start
	}

	// Reverse (we built it back-to-front).
	sort.SliceStable(pieces, func(i, j int) bool { return false })
	for i, j := 0, len(pieces)-1; i < j; i, j = i+1, j-1 {
		pieces[i], pieces[j] = pieces[j], pieces[i]
	}

	return pieces
}

// ========== Shared utilities ==========

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
