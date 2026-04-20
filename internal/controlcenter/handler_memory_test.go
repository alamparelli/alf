package controlcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	provider "github.com/alamparelli/alf/internal/ai/provider"
)

// mockMemStore implements MemoryStorer for testing.
type mockMemStore struct {
	stored []mockMemEntry
}

type mockMemEntry struct {
	Text, Type, Source string
}

func (m *mockMemStore) Store(text, memType, source string, meta map[string]any) (int64, error) {
	// Dedup: reject if already stored.
	for _, e := range m.stored {
		if e.Text == text {
			return 0, fmt.Errorf("duplicate memory detected")
		}
	}
	m.stored = append(m.stored, mockMemEntry{Text: text, Type: memType, Source: source})
	return int64(len(m.stored)), nil
}

// mockIngestProvider implements provider.Provider for testing.
type mockIngestProvider struct {
	response string
	err      error
}

func (m *mockIngestProvider) Invoke(_ context.Context, _ string, _ provider.Params, _ provider.OnProgress) (*provider.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &provider.Result{Text: m.response}, nil
}

func newIngestHandler() (*MemoryIngestHandler, *mockMemStore, *mockIngestProvider) {
	ms := &mockMemStore{}
	mp := &mockIngestProvider{}
	h := &MemoryIngestHandler{Store: ms, Provider: mp}
	return h, ms, mp
}

func doIngest(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/memory/ingest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIngest_MethodNotAllowed(t *testing.T) {
	h, _, _ := newIngestHandler()
	req := httptest.NewRequest("GET", "/api/memory/ingest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestIngest_EmptyContent(t *testing.T) {
	h, _, _ := newIngestHandler()
	rec := doIngest(h, `{"content":"","instruction":"store-as-is"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "content is required") {
		t.Errorf("expected content error, got: %s", body)
	}
}

func TestIngest_ContentTooLarge(t *testing.T) {
	h, _, _ := newIngestHandler()
	big := strings.Repeat("x", 51*1024)
	rec := doIngest(h, `{"content":"`+big+`","instruction":"store-as-is"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "50KB") {
		t.Errorf("expected size in error, got: %s", body)
	}
}

func TestIngest_StoreAsIs(t *testing.T) {
	h, ms, _ := newIngestHandler()
	rec := doIngest(h, `{"content":"fact one\nfact two\nfact three","instruction":"store-as-is"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Imported != 3 {
		t.Errorf("expected 3 imported, got %d", resp.Imported)
	}
	if len(ms.stored) != 3 {
		t.Errorf("expected 3 stored, got %d", len(ms.stored))
	}
	for _, e := range ms.stored {
		if e.Type != "fact" {
			t.Errorf("expected type=fact, got %s", e.Type)
		}
		if e.Source != "user-import" {
			t.Errorf("expected source=user-import, got %s", e.Source)
		}
	}
}

func TestIngest_StoreAsIs_BlankLines(t *testing.T) {
	h, ms, _ := newIngestHandler()
	rec := doIngest(h, `{"content":"fact one\n\n  \nfact two\n","instruction":"store-as-is"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Imported != 2 {
		t.Errorf("expected 2 imported (blank lines skipped), got %d", resp.Imported)
	}
	if len(ms.stored) != 2 {
		t.Errorf("expected 2 stored, got %d", len(ms.stored))
	}
}

func TestIngest_ClaudeExtraction(t *testing.T) {
	h, ms, mp := newIngestHandler()
	mp.response = `[{"text":"Alice is the PM","type":"fact"},{"text":"Prefers dark mode","type":"preference"}]`

	rec := doIngest(h, `{"content":"meeting notes...","instruction":"Extract key facts"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Imported != 2 {
		t.Errorf("expected 2 imported, got %d", resp.Imported)
	}
	if len(ms.stored) != 2 {
		t.Errorf("expected 2 stored, got %d", len(ms.stored))
	}
	if ms.stored[0].Type != "fact" {
		t.Errorf("expected first type=fact, got %s", ms.stored[0].Type)
	}
	if ms.stored[1].Type != "preference" {
		t.Errorf("expected second type=preference, got %s", ms.stored[1].Type)
	}
}

func TestIngest_ClaudeMalformedJSON(t *testing.T) {
	h, _, mp := newIngestHandler()
	mp.response = `This is not JSON at all`

	rec := doIngest(h, `{"content":"notes","instruction":"Extract key facts"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "extraction failed") {
		t.Errorf("expected generic error, got: %s", body)
	}
}

func TestIngest_ClaudeEmptyArray(t *testing.T) {
	h, _, mp := newIngestHandler()
	mp.response = `[]`

	rec := doIngest(h, `{"content":"nothing useful","instruction":"Extract key facts"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp.Imported != 0 || resp.Skipped != 0 {
		t.Errorf("expected 0/0, got imported=%d skipped=%d", resp.Imported, resp.Skipped)
	}
}

func TestIngest_Dedup(t *testing.T) {
	h, _, _ := newIngestHandler()

	// First import.
	rec := doIngest(h, `{"content":"fact one\nfact two","instruction":"store-as-is"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("first: expected 200, got %d", rec.Code)
	}
	var resp1 ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp1)
	if resp1.Imported != 2 {
		t.Errorf("first: expected 2 imported, got %d", resp1.Imported)
	}

	// Second import with same content.
	rec2 := doIngest(h, `{"content":"fact one\nfact two","instruction":"store-as-is"}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second: expected 200, got %d", rec2.Code)
	}
	var resp2 ingestResponse
	json.NewDecoder(rec2.Body).Decode(&resp2)
	if resp2.Skipped != 2 {
		t.Errorf("second: expected 2 skipped, got %d", resp2.Skipped)
	}
	if resp2.Imported != 0 {
		t.Errorf("second: expected 0 imported, got %d", resp2.Imported)
	}
}

func TestIngest_ClaudeCodeBlockWrapped(t *testing.T) {
	h, ms, mp := newIngestHandler()
	mp.response = "```json\n[{\"text\":\"wrapped fact\",\"type\":\"decision\"}]\n```"

	rec := doIngest(h, `{"content":"notes","instruction":"Extract decisions"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", resp.Imported)
	}
	if ms.stored[0].Type != "decision" {
		t.Errorf("expected type=decision, got %s", ms.stored[0].Type)
	}
}

func TestIngest_ClaudeProseWrappedJSON(t *testing.T) {
	h, ms, mp := newIngestHandler()
	mp.response = "Here are the extracted facts:\n\n[{\"text\":\"The project uses Go\",\"type\":\"fact\"}]\n\nLet me know if you need more."

	rec := doIngest(h, `{"content":"notes about Go project","instruction":"Extract key facts"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp ingestResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", resp.Imported)
	}
	if len(ms.stored) != 1 || ms.stored[0].Text != "The project uses Go" {
		t.Errorf("unexpected stored: %+v", ms.stored)
	}
}

func TestIngest_InvalidType_DefaultsToFact(t *testing.T) {
	h, ms, mp := newIngestHandler()
	mp.response = `[{"text":"some info","type":"random_type"}]`

	rec := doIngest(h, `{"content":"notes","instruction":"Extract"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if ms.stored[0].Type != "fact" {
		t.Errorf("expected type=fact for unknown type, got %s", ms.stored[0].Type)
	}
}

// --- Context destination tests ---

type mockContextStore struct {
	files map[string][]byte
}

func (m *mockContextStore) List() ([]ResourceMeta, error) { return nil, nil }
func (m *mockContextStore) Get(name string) ([]byte, error) {
	if d, ok := m.files[name]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("not found")
}
func (m *mockContextStore) Put(name string, data []byte) error {
	m.files[name] = data
	return nil
}
func (m *mockContextStore) Delete(name string) error {
	delete(m.files, name)
	return nil
}

func newContextIngestHandler() (*MemoryIngestHandler, *mockContextStore, *mockIngestProvider) {
	ms := &mockMemStore{}
	mp := &mockIngestProvider{}
	cs := &mockContextStore{files: map[string][]byte{}}
	h := &MemoryIngestHandler{Store: ms, Provider: mp, ContextStore: cs}
	return h, cs, mp
}

func TestIngest_Context_StoreAsIs(t *testing.T) {
	h, cs, _ := newContextIngestHandler()
	rec := doIngest(h, `{"content":"some notes\nmore notes","instruction":"store-as-is","destination":"context","file_name":"my-notes"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	data, ok := cs.files["my-notes"]
	if !ok {
		t.Fatal("expected file written to context store")
	}
	if string(data) != "some notes\nmore notes" {
		t.Errorf("unexpected content: %q", string(data))
	}

	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["file_name"] != "my-notes.md" {
		t.Errorf("expected file_name=my-notes.md, got %v", resp["file_name"])
	}
}

func TestIngest_Context_Summarize(t *testing.T) {
	h, cs, mp := newContextIngestHandler()
	mp.response = "- Go is used\n- Deploy on Friday"

	rec := doIngest(h, `{"content":"project notes","instruction":"summarize","destination":"context","file_name":"project"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	data, ok := cs.files["project"]
	if !ok {
		t.Fatal("expected file written to context store")
	}
	expected := "- Go is used\n- Deploy on Friday\n"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, string(data))
	}
}

func TestIngest_Context_MissingFileName(t *testing.T) {
	h, _, _ := newContextIngestHandler()
	rec := doIngest(h, `{"content":"notes","instruction":"store-as-is","destination":"context","file_name":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestIngest_Context_InvalidFileName(t *testing.T) {
	h, _, _ := newContextIngestHandler()
	rec := doIngest(h, `{"content":"notes","instruction":"store-as-is","destination":"context","file_name":"bad name!"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestIngest_Context_ProtectedFile(t *testing.T) {
	for _, name := range []string{"soul", "mood", "index", "toolbox"} {
		h, _, _ := newContextIngestHandler()
		rec := doIngest(h, `{"content":"notes","instruction":"store-as-is","destination":"context","file_name":"`+name+`"}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", name, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "protected") {
			t.Errorf("%s: expected protected error, got: %s", name, body)
		}
	}
}

func TestIngest_Context_NoContextStore(t *testing.T) {
	h, _, _ := newIngestHandler() // no ContextStore
	rec := doIngest(h, `{"content":"notes","instruction":"store-as-is","destination":"context","file_name":"test"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}
