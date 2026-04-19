package memstore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func skipIfNoGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}
}

// newTestExtractor wires an Extractor around a temp stateDir and a mock
// provider with a fixed tierResolver. No git repo is required unless the
// caller invokes gitCommand / HasChanges.
func newTestExtractor(t *testing.T, prov ExtractorProvider) *Extractor {
	t.Helper()
	dir := t.TempDir()
	ctxDir := t.TempDir()
	return &Extractor{
		dataDir:      dir,
		stateDir:     ctxDir,
		statePath:    filepath.Join(ctxDir, "state.json"),
		timeout:      time.Minute,
		msgThreshold: 10,
		provider:     prov,
		tierResolver: func() string { return "test-model" },
		msgCounts:    map[string]int{},
	}
}

func TestNewConsolidator_DefaultTimeout(t *testing.T) {
	c := NewConsolidator(nil, nil, nil, 0)
	if c.timeout != 10*time.Minute {
		t.Errorf("expected 10m default timeout, got %s", c.timeout)
	}
}

func TestNewConsolidator_CustomTimeout(t *testing.T) {
	c := NewConsolidator(nil, nil, nil, 42*time.Second)
	if c.timeout != 42*time.Second {
		t.Errorf("expected 42s timeout, got %s", c.timeout)
	}
}

func TestIdentifyActions_CleanJSON(t *testing.T) {
	prov := &mockProvider{
		response: `[{"action":"delete","ids":[1,2],"reason":"duplicate"}]`,
	}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(nil, ext, prov, time.Minute)

	actions, err := c.identifyActions("[ID:1] ...\n[ID:2] ...")
	if err != nil {
		t.Fatalf("identifyActions: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "delete" || len(actions[0].IDs) != 2 {
		t.Errorf("unexpected actions: %+v", actions)
	}
	if prov.calls[0].Params.Model != "test-model" {
		t.Errorf("expected tier-resolved model, got %q", prov.calls[0].Params.Model)
	}
}

func TestIdentifyActions_MarkdownWrapped(t *testing.T) {
	prov := &mockProvider{response: "```json\n[{\"action\":\"delete\",\"ids\":[3],\"reason\":\"stale\"}]\n```"}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(nil, ext, prov, time.Minute)

	actions, err := c.identifyActions("list")
	if err != nil {
		t.Fatalf("identifyActions: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "delete" {
		t.Errorf("markdown-wrapped JSON not parsed: %+v", actions)
	}
}

func TestIdentifyActions_EmbeddedInText(t *testing.T) {
	prov := &mockProvider{
		response: "Sure, here are the actions:\n[{\"action\":\"delete\",\"ids\":[9],\"reason\":\"dup\"}]\nHope that helps.",
	}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(nil, ext, prov, time.Minute)

	actions, err := c.identifyActions("list")
	if err != nil {
		t.Fatalf("identifyActions: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("expected 1 action from embedded JSON, got %+v", actions)
	}
}

func TestIdentifyActions_InvalidJSON(t *testing.T) {
	prov := &mockProvider{response: "I don't think anything needs changes."}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(nil, ext, prov, time.Minute)

	_, err := c.identifyActions("list")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse consolidation response") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestIdentifyActions_NoTierAvailable(t *testing.T) {
	prov := &mockProvider{response: "[]"}
	ext := newTestExtractor(t, prov)
	ext.tierResolver = nil
	c := NewConsolidator(nil, ext, prov, time.Minute)

	_, err := c.identifyActions("list")
	if err == nil {
		t.Fatal("expected error when no tier resolver")
	}
	if len(prov.calls) != 0 {
		t.Errorf("provider should not be invoked without tier, got %d calls", len(prov.calls))
	}
}

func TestIdentifyActions_ResolverReturnsEmpty(t *testing.T) {
	prov := &mockProvider{response: "[]"}
	ext := newTestExtractor(t, prov)
	ext.tierResolver = func() string { return "" }
	c := NewConsolidator(nil, ext, prov, time.Minute)

	_, err := c.identifyActions("list")
	if err == nil {
		t.Fatal("expected error when tier resolver returns empty")
	}
}

func TestIdentifyActions_ProviderError(t *testing.T) {
	prov := &mockProvider{err: fmt.Errorf("timeout")}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(nil, ext, prov, time.Minute)

	_, err := c.identifyActions("list")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "LLM consolidation") {
		t.Errorf("unexpected error: %v", err)
	}
}

// distinctFacts are intentionally unrelated strings so that the lexical
// Jaccard dedup (text threshold 0.7) does not reject any insert. Eight
// entries cover the max bench size used in these tests.
var distinctFacts = []string{
	"alice works at acme corporation",
	"the deployment window is tuesday afternoon",
	"our postgres version is fourteen point two",
	"the customer prefers outlook over gmail for invites",
	"the quarterly budget review happens every march",
	"berlin office uses a different vpn endpoint",
	"marketing owns the homepage hero copy",
	"release notes ship in pdf to enterprise clients",
}

// seedMemories inserts n distinct memories into store. Returns their IDs in
// the order of insertion. Each text is unrelated to the next so dedup does
// not reject any entry.
func seedMemories(t *testing.T, s *Store, n int) []int64 {
	t.Helper()
	if n > len(distinctFacts) {
		t.Fatalf("seedMemories: requested %d, only %d distinct facts available", n, len(distinctFacts))
	}
	var ids []int64
	for i := 0; i < n; i++ {
		id, err := s.Store(distinctFacts[i], "fact", "test", nil)
		if err != nil {
			t.Fatalf("Store[%d]: %v", i, err)
		}
		ids = append(ids, id)
		// Ensure strictly-ordered created_at timestamps so Recent() order is deterministic.
		time.Sleep(2 * time.Millisecond)
	}
	return ids
}

func TestConsolidate_SkipsWhenFewMemories(t *testing.T) {
	store := newTestStore(t)
	seedMemories(t, store, 3)

	prov := &mockProvider{response: `[{"action":"delete","ids":[1],"reason":"x"}]`}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if len(prov.calls) != 0 {
		t.Errorf("provider should not be called with < 5 memories, got %d calls", len(prov.calls))
	}
	if store.Count() != 3 {
		t.Errorf("store should be untouched: got %d memories", store.Count())
	}
}

func TestConsolidate_NoActions(t *testing.T) {
	store := newTestStore(t)
	seedMemories(t, store, 6)

	prov := &mockProvider{response: "[]"}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if len(prov.calls) != 1 {
		t.Errorf("expected 1 provider call, got %d", len(prov.calls))
	}
	if store.Count() != 6 {
		t.Errorf("store should be untouched: got %d memories", store.Count())
	}
}

func TestConsolidate_Delete(t *testing.T) {
	store := newTestStore(t)
	ids := seedMemories(t, store, 6)

	// Delete the first two memories.
	resp := fmt.Sprintf(`[{"action":"delete","ids":[%d,%d],"reason":"dup"}]`, ids[0], ids[1])
	prov := &mockProvider{response: resp}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if got := store.Count(); got != 4 {
		t.Errorf("expected 4 remaining memories, got %d", got)
	}
}

func TestConsolidate_Merge(t *testing.T) {
	store := newTestStore(t)
	ids := seedMemories(t, store, 6)

	resp := fmt.Sprintf(`[{"action":"merge","ids":[%d,%d],"merged_text":"unified new text alpha beta","new_type":"fact","reason":"consolidated"}]`, ids[0], ids[1])
	prov := &mockProvider{response: resp}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	// 6 original - 2 deleted + 1 merged = 5.
	if got := store.Count(); got != 5 {
		t.Errorf("expected 5 memories after merge, got %d", got)
	}
}

func TestConsolidate_MergeRejectsInvalid(t *testing.T) {
	store := newTestStore(t)
	ids := seedMemories(t, store, 6)

	// Too few IDs AND empty merged_text — must be skipped.
	resp := fmt.Sprintf(`[{"action":"merge","ids":[%d],"merged_text":"","reason":"bad"}]`, ids[0])
	prov := &mockProvider{response: resp}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if got := store.Count(); got != 6 {
		t.Errorf("invalid merge should be a no-op, got count=%d", got)
	}
}

func TestConsolidate_Retype(t *testing.T) {
	store := newTestStore(t)
	ids := seedMemories(t, store, 6)

	resp := fmt.Sprintf(`[{"action":"retype","ids":[%d],"new_type":"preference","reason":"wrong type"}]`, ids[0])
	prov := &mockProvider{response: resp}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	// Count stays the same (delete + re-insert).
	if got := store.Count(); got != 6 {
		t.Errorf("retype should preserve count, got %d", got)
	}
	// The memory should now be a "preference".
	mems, err := store.Recent(365, 500)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	foundPref := false
	for _, m := range mems {
		if m.Type == "preference" {
			foundPref = true
			break
		}
	}
	if !foundPref {
		t.Errorf("retype to preference not applied")
	}
}

func TestConsolidate_RetypeRejectsInvalid(t *testing.T) {
	store := newTestStore(t)
	seedMemories(t, store, 6)

	// Missing new_type — skipped.
	resp := `[{"action":"retype","ids":[1],"new_type":"","reason":"bad"}]`
	prov := &mockProvider{response: resp}
	ext := newTestExtractor(t, prov)
	c := NewConsolidator(store, ext, prov, time.Minute)

	if err := c.consolidate(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if got := store.Count(); got != 6 {
		t.Errorf("invalid retype should be a no-op, got count=%d", got)
	}
}

func TestRunOnce_ConsolidatesWithoutExtraction(t *testing.T) {
	skipIfNoGit(t)
	store := newTestStore(t)
	seedMemories(t, store, 6)

	// Init a git repo and commit once so HasChanges returns false after state is saved.
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	prov := &mockProvider{response: "[]"}
	ext := newTestExtractor(t, prov)
	ext.dataDir = dir
	ext.saveState(head)

	c := NewConsolidator(store, ext, prov, time.Minute)
	if err := c.RunOnce(); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(prov.calls) != 1 {
		// exactly one call: consolidation (no extraction because HasChanges is false).
		t.Errorf("expected 1 provider call (consolidation only), got %d", len(prov.calls))
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// --- Extractor HasChanges / LoadState coverage ---

func TestHasChanges_NoState(t *testing.T) {
	skipIfNoGit(t)
	ext := newTestExtractor(t, &mockProvider{})
	// Fresh extractor with no state: must report HasChanges true regardless of git state.
	if !ext.HasChanges() {
		t.Error("expected HasChanges=true when no state recorded")
	}
}

func TestHasChanges_GitError(t *testing.T) {
	ext := newTestExtractor(t, &mockProvider{})
	// Set a non-empty state so we hit the git path.
	ext.saveState("some-hash")
	// dataDir is a tempdir that is NOT a git repo; rev-parse must fail.
	if !ext.HasChanges() {
		t.Error("expected HasChanges=true when git rev-parse fails")
	}
}

func TestHasChanges_CleanTree(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	ext := newTestExtractor(t, &mockProvider{})
	ext.dataDir = dir
	ext.saveState(head)

	if ext.HasChanges() {
		t.Error("expected HasChanges=false on clean tree at recorded HEAD")
	}
}

func TestHasChanges_HeadMoved(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")

	ext := newTestExtractor(t, &mockProvider{})
	ext.dataDir = dir
	ext.saveState("stale-hash")

	if !ext.HasChanges() {
		t.Error("expected HasChanges=true when HEAD has moved past recorded hash")
	}
}

func TestHasChanges_UncommittedEdits(t *testing.T) {
	skipIfNoGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@t")
	runGit(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "init")
	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	// Touch the file after the commit (working-tree diff).
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("xx"), 0o644)

	ext := newTestExtractor(t, &mockProvider{})
	ext.dataDir = dir
	ext.saveState(head)

	if !ext.HasChanges() {
		t.Error("expected HasChanges=true when working tree is dirty")
	}
}

func TestLoadState_Exported(t *testing.T) {
	ext := newTestExtractor(t, &mockProvider{})
	ext.saveState("abc")

	st := ext.LoadState()
	if st.LastHash != "abc" {
		t.Errorf("LoadState: expected hash=abc, got %q", st.LastHash)
	}
}
