package tooling

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func setupIntegrityTest(t *testing.T) (string, *IntegrityGuard, string) {
	t.Helper()
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(toolsDir, 0o755)

	ig, err := NewIntegrityGuard(dir, nil)
	if err != nil {
		t.Fatalf("NewIntegrityGuard: %v", err)
	}
	return dir, ig, toolsDir
}

func writeTool(t *testing.T, toolsDir, name, content string) string {
	t.Helper()
	path := filepath.Join(toolsDir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write tool: %v", err)
	}
	return path
}

// scanOnce triggers a non-initial scan (simulates a poll tick).
func scanOnce(ig *IntegrityGuard) {
	ig.scan(false)
}

// ageManifest backdates all manifest entries past the grace period so that
// modifications trigger quarantine in tests (which run in <1s).
func ageManifest(ig *IntegrityGuard) {
	ig.mu.Lock()
	defer ig.mu.Unlock()
	old := time.Now().Add(-60 * time.Second).UTC().Format(time.RFC3339)
	for name, entry := range ig.manifest {
		entry.FirstSeen = old
		ig.manifest[name] = entry
	}
}

func TestIntegrity_NewTool_Registered(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")

	// Initial scan registers all tools.
	ig.scan(true)

	ig.mu.Lock()
	entry, ok := ig.manifest["hello"]
	ig.mu.Unlock()
	if !ok {
		t.Fatal("manifest entry not created")
	}
	if entry.ExeHash == "" {
		t.Fatal("exe hash is empty")
	}
}

func TestIntegrity_UnchangedTool_NotQuarantined(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")

	ig.scan(true) // baseline
	scanOnce(ig)  // poll — no change

	if ig.IsQuarantined("hello") {
		t.Fatal("unchanged tool should not be quarantined")
	}
}

func TestIntegrity_ModifiedTool_SafeChange_AutoApproved(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")

	ig.scan(true) // baseline
	ageManifest(ig)

	// Safe modification — should be auto-approved, not quarantined.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho updated"), 0o755)

	scanOnce(ig)

	if ig.IsQuarantined("hello") {
		t.Fatal("safe modification should be auto-approved, not quarantined")
	}

	// Manifest should have new hash.
	ig.mu.Lock()
	entry := ig.manifest["hello"]
	ig.mu.Unlock()
	hash, _ := hashFile(path)
	if entry.ExeHash != hash {
		t.Fatal("manifest should reflect auto-approved change")
	}
}

func TestIntegrity_DangerousModification_Quarantined(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")

	ig.scan(true) // baseline
	ageManifest(ig)

	// Dangerous modification — contains eval().
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)

	scanOnce(ig)

	if !ig.IsQuarantined("hello") {
		t.Fatal("dangerous modification should be quarantined")
	}

	// Verify quarantined copy exists in daemon dir.
	qPath := filepath.Join(dir, ".daemon", "tool-quarantine", "hello")
	if _, err := os.Stat(qPath); os.IsNotExist(err) {
		t.Fatal("quarantined file should exist in daemon dir")
	}

	// Verify backup was restored (original content).
	data, _ := os.ReadFile(path)
	if string(data) != "#!/bin/sh\necho hello" {
		t.Fatalf("backup not restored, got: %s", string(data))
	}
}

func TestIntegrity_Check_ReturnsQuarantined(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")
	ig.scan(true)
	ageManifest(ig)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	if err := ig.Check(path); err != ErrToolQuarantined {
		t.Fatalf("expected ErrToolQuarantined, got: %v", err)
	}
}

func TestIntegrity_ApproveModified(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")
	ig.scan(true)
	ageManifest(ig)

	// Use dangerous content to trigger quarantine.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	if !ig.IsQuarantined("hello") {
		t.Fatal("dangerous tool should be quarantined before approve")
	}

	if err := ig.ApproveModified("hello"); err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	if ig.IsQuarantined("hello") {
		t.Fatal("tool should not be quarantined after approval")
	}

	// Active tool should be the modified version.
	data, _ := os.ReadFile(path)
	if string(data) != "#!/usr/bin/env python3\neval(input())" {
		t.Fatalf("approved version not active, got: %s", string(data))
	}

	// Quarantined copy should be cleaned up.
	qPath := filepath.Join(dir, ".daemon", "tool-quarantine", "hello")
	if _, err := os.Stat(qPath); !os.IsNotExist(err) {
		t.Fatal("quarantined file should be removed after approval")
	}

	// Next scan should not re-quarantine (content unchanged).
	scanOnce(ig)
	if ig.IsQuarantined("hello") {
		t.Fatal("approved tool re-quarantined on next scan")
	}
}

func TestIntegrity_RevertTool(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")
	ig.scan(true)
	ageManifest(ig)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	if err := ig.RevertTool("hello"); err != nil {
		t.Fatalf("revert failed: %v", err)
	}

	if ig.IsQuarantined("hello") {
		t.Fatal("tool should not be quarantined after revert")
	}

	data, _ := os.ReadFile(path)
	if string(data) != "#!/bin/sh\necho hello" {
		t.Fatalf("original not preserved, got: %s", string(data))
	}

	qPath := filepath.Join(dir, ".daemon", "tool-quarantine", "hello")
	if _, err := os.Stat(qPath); !os.IsNotExist(err) {
		t.Fatal("quarantined file should be removed after revert")
	}

	// Next scan should not re-quarantine (mtime cache updated).
	scanOnce(ig)
	if ig.IsQuarantined("hello") {
		t.Fatal("reverted tool re-quarantined on next scan")
	}
}

func TestIntegrity_SchemaChange_AutoApproved(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "mytool", "#!/bin/sh\necho ok")
	schemaPath := filepath.Join(toolsDir, "mytool.json")
	os.WriteFile(schemaPath, []byte(`{"name":"mytool"}`), 0o644)

	ig.scan(true)
	ageManifest(ig)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(schemaPath, []byte(`{"name":"mytool","description":"updated"}`), 0o644)
	// Touch the exe so mtime changes and scan rechecks.
	exe := filepath.Join(toolsDir, "mytool")
	data, _ := os.ReadFile(exe)
	os.WriteFile(exe, data, 0o755)

	scanOnce(ig)

	// Schema-only change with safe content → auto-approved, not quarantined.
	if ig.IsQuarantined("mytool") {
		t.Fatal("safe schema change should be auto-approved, not quarantined")
	}
}

func TestIntegrity_ManifestPersistence(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")
	ig.scan(true)

	// Create a new guard reading the same manifest.
	ig2, err := NewIntegrityGuard(dir, nil)
	if err != nil {
		t.Fatalf("second guard: %v", err)
	}

	ig2.mu.Lock()
	_, ok := ig2.manifest["hello"]
	ig2.mu.Unlock()
	if !ok {
		t.Fatal("manifest not persisted across reload")
	}
}

func TestIntegrity_DangerousChange_NoNotification(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(toolsDir, 0o755)

	var notified bool
	ig, _ := NewIntegrityGuard(dir, func(tool, oldHash, newHash string) {
		notified = true
	})

	path := writeTool(t, toolsDir, "test", "original")
	ig.scan(true)
	ageManifest(ig)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("import os\nos.system('rm -rf /')"), 0o755)
	scanOnce(ig)

	// Quarantine should happen but no notification (heartbeat picks it up).
	if notified {
		t.Fatal("notify func should NOT be called — log-only mode")
	}
	if _, q := ig.quarantined["test"]; !q {
		t.Fatal("dangerous tool should still be quarantined")
	}
}

func TestIntegrity_ConcurrentScan(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "concurrent", "#!/bin/sh\necho ok")
	ig.scan(true)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanOnce(ig)
		}()
	}
	wg.Wait()

	if ig.IsQuarantined("concurrent") {
		t.Fatal("unchanged tool should not be quarantined under concurrent scans")
	}
}

func TestIntegrity_DeletedTool_RemovedFromManifest(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "ephemeral", "#!/bin/sh\necho bye")
	ig.scan(true)

	os.Remove(path)
	scanOnce(ig)

	ig.mu.Lock()
	_, ok := ig.manifest["ephemeral"]
	ig.mu.Unlock()
	if ok {
		t.Fatal("deleted tool should be removed from manifest")
	}
}

func TestIntegrity_WatchAndStop(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "watched", "#!/bin/sh\necho ok")

	ig.Watch(50 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	ig.Stop()

	ig.mu.Lock()
	_, ok := ig.manifest["watched"]
	ig.mu.Unlock()
	if !ok {
		t.Fatal("watch should have registered the tool")
	}
}

func TestIntegrity_ApproveNotQuarantined_Error(t *testing.T) {
	_, ig, _ := setupIntegrityTest(t)
	if err := ig.ApproveModified("nonexistent"); err == nil {
		t.Fatal("approve of non-quarantined tool should error")
	}
}

func TestIntegrity_RevertNotQuarantined_Error(t *testing.T) {
	_, ig, _ := setupIntegrityTest(t)
	if err := ig.RevertTool("nonexistent"); err == nil {
		t.Fatal("revert of non-quarantined tool should error")
	}
}

func TestIntegrity_Quarantined_List(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "a", "original")
	ig.scan(true)
	ageManifest(ig)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("import os\nos.system('whoami')"), 0o755)
	scanOnce(ig)

	list := ig.Quarantined()
	if len(list) != 1 {
		t.Fatalf("expected 1 quarantined tool, got %d", len(list))
	}
	if list[0].Name != "a" {
		t.Fatalf("expected tool 'a', got %q", list[0].Name)
	}
}

func TestIsUserTool(t *testing.T) {
	if !IsUserTool("/data/tools/hello", "/data") {
		t.Error("should be user tool")
	}
	if IsUserTool("/data/tools.d/hello", "/data") {
		t.Error("tools.d should not be user tool")
	}
	if IsUserTool("/other/tools/hello", "/data") {
		t.Error("different data dir should not match")
	}
}

func TestIntegrity_RewriteWhileQuarantined_ReRestored(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho original")
	ig.scan(true)
	ageManifest(ig)

	// First modification with dangerous pattern → quarantine.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	if !ig.IsQuarantined("hello") {
		t.Fatal("should be quarantined")
	}

	// LLM writes again while quarantined.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval('more')"), 0o755)
	scanOnce(ig)

	// Should still be quarantined and backup re-restored.
	if !ig.IsQuarantined("hello") {
		t.Fatal("should still be quarantined")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "#!/bin/sh\necho original" {
		t.Fatalf("backup not re-restored, got: %s", string(data))
	}
}

func TestIntegrity_BaselineScan_AcceptsMismatch(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "existing", "#!/bin/sh\necho v1")
	ig.scan(true)

	// Simulate daemon restart with a modified tool.
	time.Sleep(10 * time.Millisecond)
	path := filepath.Join(toolsDir, "existing")
	os.WriteFile(path, []byte("#!/bin/sh\necho v2"), 0o755)

	// New guard does initial scan — should accept, not quarantine.
	ig2, _ := NewIntegrityGuard(dir, nil)
	ig2.scan(true)

	if ig2.IsQuarantined("existing") {
		t.Fatal("initial scan should accept current state, not quarantine")
	}
}

// --- TOCTOU regression tests (SEC-002) ---

// TestVerify_BlocksModifiedBetweenScans verifies that Verify() catches a tool
// modified after the last scan but before execution. This is the TOCTOU fix:
// Check() would pass (map lookup says "not quarantined"), but Verify() hashes
// the file and detects the mismatch.
func TestVerify_BlocksModifiedBetweenScans(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "mytool", "#!/bin/sh\necho safe")

	// Scan to baseline the tool.
	ig.scan(true)
	ageManifest(ig)

	// Simulate LLM modifying the tool AFTER scan but BEFORE execution.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho pwned"), 0o755)

	// Check() would pass (not quarantined yet — next scan hasn't run).
	if err := ig.Check(path); err != nil {
		t.Fatalf("Check() should pass (no scan since modification): %v", err)
	}

	// Verify() must catch the modification at execution time.
	if err := ig.Verify(path); err == nil {
		t.Fatal("Verify() should block tool modified between scans (TOCTOU)")
	}
}

// TestVerify_AllowsUnmodifiedTool verifies that Verify() allows a tool
// whose hash matches the manifest.
func TestVerify_AllowsUnmodifiedTool(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "goodtool", "#!/bin/sh\necho ok")

	ig.scan(true)

	if err := ig.Verify(path); err != nil {
		t.Fatalf("Verify() should allow unmodified tool: %v", err)
	}
}

// TestVerify_AllowsNewToolNotYetScanned verifies that Verify() allows a
// brand new tool that hasn't been scanned yet (not in manifest).
func TestVerify_AllowsNewToolNotYetScanned(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	ig.scan(true) // baseline with no tools

	// Create tool after scan — not yet in manifest.
	path := writeTool(t, toolsDir, "newtool", "#!/bin/sh\necho new")

	if err := ig.Verify(path); err != nil {
		t.Fatalf("Verify() should allow new tool not yet in manifest: %v", err)
	}
}

// TestVerify_BlocksQuarantinedTool verifies that Verify() blocks quarantined
// tools (same as Check, but Verify is now the single entry point for executor).
func TestVerify_BlocksQuarantinedTool(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "qtool", "#!/bin/sh\necho v1")
	ig.scan(true)
	ageManifest(ig)

	// Modify with dangerous content to trigger quarantine.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	ig.scan(false)

	if !ig.IsQuarantined("qtool") {
		t.Fatal("tool should be quarantined after modification + scan")
	}

	if err := ig.Verify(path); err == nil {
		t.Fatal("Verify() should block quarantined tool")
	}
}

// --- Regression tests for #221 ---

// TestRegression221_NewToolGracePeriod verifies that a newly created tool
// modified within 30s is NOT quarantined (LLM creation pattern: write then refine).
func TestRegression221_NewToolGracePeriod(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)

	// Simulate LLM creating a tool (no initial baseline — tool didn't exist).
	path := writeTool(t, toolsDir, "newtool", "#!/bin/sh\necho v1")
	scanOnce(ig) // registers as new tool

	ig.mu.Lock()
	_, exists := ig.manifest["newtool"]
	ig.mu.Unlock()
	if !exists {
		t.Fatal("tool should be registered after first scan")
	}

	// LLM modifies it immediately (within grace period).
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho v2"), 0o755)
	scanOnce(ig)

	if ig.IsQuarantined("newtool") {
		t.Fatal("newly created tool modified within grace period should NOT be quarantined (#221)")
	}

	// Manifest should have the updated hash.
	ig.mu.Lock()
	entry := ig.manifest["newtool"]
	ig.mu.Unlock()
	hash, _ := hashFile(path)
	if entry.ExeHash != hash {
		t.Fatal("manifest should reflect the latest version during grace period")
	}
}

// TestRegression221_QuarantinePersistsReboot verifies that quarantine state
// survives daemon restart (new IntegrityGuard instance reads persisted state).
func TestRegression221_QuarantinePersistsReboot(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho original")
	ig.scan(true)
	ageManifest(ig)

	// Modify with dangerous content to trigger quarantine.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	if !ig.IsQuarantined("hello") {
		t.Fatal("tool should be quarantined")
	}

	// Simulate daemon restart — create new guard.
	ig2, err := NewIntegrityGuard(dir, nil)
	if err != nil {
		t.Fatalf("new guard: %v", err)
	}

	// Quarantine state should be restored from JSON file.
	if !ig2.IsQuarantined("hello") {
		t.Fatal("quarantine state should persist across daemon restart (#221)")
	}

	// /tool keep should work on the restarted guard.
	if err := ig2.ApproveModified("hello"); err != nil {
		t.Fatalf("/tool keep failed after restart: %v (#221)", err)
	}

	if ig2.IsQuarantined("hello") {
		t.Fatal("tool should not be quarantined after approval")
	}
}

// TestRegression221_QuarantineJSONPersistence verifies the JSON state file
// is written and cleaned up correctly.
func TestRegression221_QuarantineJSONPersistence(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "tool1", "#!/bin/sh\necho v1")
	ig.scan(true)
	ageManifest(ig)

	// Quarantine with dangerous content.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	// JSON file should exist.
	jsonPath := filepath.Join(dir, ".daemon", "tool-quarantine.json")
	if _, err := os.Stat(jsonPath); os.IsNotExist(err) {
		t.Fatal("tool-quarantine.json should exist after quarantine")
	}

	// Approve — JSON should be updated (empty map).
	ig.ApproveModified("tool1")

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal("should be able to read quarantine JSON after approve")
	}
	if strings.Contains(string(data), "tool1") {
		t.Fatal("tool1 should be removed from quarantine JSON after approve")
	}
}

// TestQuarantine_LockdownPerms verifies that quarantined tools have execute
// stripped and are restored on approve/revert.
func TestQuarantine_LockdownPerms(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho safe")
	ig.scan(true)
	ageManifest(ig)

	// Trigger quarantine with dangerous content.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\neval(input())"), 0o755)
	scanOnce(ig)

	// After quarantine, tool should NOT be executable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal("tool file should exist after quarantine (restored backup)")
	}
	mode := info.Mode().Perm()
	if mode&0o111 != 0 {
		t.Errorf("quarantined tool should have no execute bits, got %o", mode)
	}

	// Approve — should restore execute permission.
	ig.ApproveModified("hello")
	info, _ = os.Stat(path)
	mode = info.Mode().Perm()
	if mode&0o111 == 0 {
		t.Errorf("approved tool should be executable, got %o", mode)
	}
}

// TestQuarantine_RevertRestoresPerms verifies revert also unlocks the tool.
func TestQuarantine_RevertRestoresPerms(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho safe")
	ig.scan(true)
	ageManifest(ig)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/usr/bin/env python3\nos.system('rm -rf /')"), 0o755)
	scanOnce(ig)

	// Locked down.
	info, _ := os.Stat(path)
	if info.Mode().Perm()&0o111 != 0 {
		t.Error("quarantined tool should not be executable")
	}

	// Revert — should restore perms.
	ig.RevertTool("hello")
	info, _ = os.Stat(path)
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("reverted tool should be executable")
	}
}
