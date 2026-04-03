package tooling

import (
	"os"
	"path/filepath"
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

func TestIntegrity_ModifiedTool_Quarantined(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")

	ig.scan(true) // baseline

	// Modify the tool (with different mtime to bypass fast path).
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho HACKED"), 0o755)

	scanOnce(ig)

	if !ig.IsQuarantined("hello") {
		t.Fatal("modified tool should be quarantined")
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

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho HACKED"), 0o755)
	scanOnce(ig)

	if err := ig.Check(path); err != ErrToolQuarantined {
		t.Fatalf("expected ErrToolQuarantined, got: %v", err)
	}
}

func TestIntegrity_ApproveModified(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")
	ig.scan(true)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho v2"), 0o755)
	scanOnce(ig)

	if err := ig.ApproveModified("hello"); err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	if ig.IsQuarantined("hello") {
		t.Fatal("tool should not be quarantined after approval")
	}

	// Active tool should be the modified version.
	data, _ := os.ReadFile(path)
	if string(data) != "#!/bin/sh\necho v2" {
		t.Fatalf("approved version not active, got: %s", string(data))
	}

	// Quarantined copy should be cleaned up.
	qPath := filepath.Join(dir, ".daemon", "tool-quarantine", "hello")
	if _, err := os.Stat(qPath); !os.IsNotExist(err) {
		t.Fatal("quarantined file should be removed after approval")
	}

	// Next scan should not re-quarantine.
	scanOnce(ig)
	if ig.IsQuarantined("hello") {
		t.Fatal("approved tool re-quarantined on next scan")
	}
}

func TestIntegrity_RevertTool(t *testing.T) {
	dir, ig, toolsDir := setupIntegrityTest(t)
	path := writeTool(t, toolsDir, "hello", "#!/bin/sh\necho hello")
	ig.scan(true)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho HACKED"), 0o755)
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

func TestIntegrity_SchemaChange_Quarantined(t *testing.T) {
	_, ig, toolsDir := setupIntegrityTest(t)
	writeTool(t, toolsDir, "mytool", "#!/bin/sh\necho ok")
	schemaPath := filepath.Join(toolsDir, "mytool.json")
	os.WriteFile(schemaPath, []byte(`{"name":"mytool"}`), 0o644)

	ig.scan(true)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(schemaPath, []byte(`{"name":"mytool","description":"hacked"}`), 0o644)
	// Touch the exe so mtime changes and scan rechecks.
	exe := filepath.Join(toolsDir, "mytool")
	data, _ := os.ReadFile(exe)
	os.WriteFile(exe, data, 0o755)

	scanOnce(ig)

	if !ig.IsQuarantined("mytool") {
		t.Fatal("schema change should trigger quarantine")
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

func TestIntegrity_NotifyFunc_Called(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(toolsDir, 0o755)

	var notified bool
	var notifiedTool string
	ig, _ := NewIntegrityGuard(dir, func(tool, oldHash, newHash string) {
		notified = true
		notifiedTool = tool
	})

	path := writeTool(t, toolsDir, "test", "original")
	ig.scan(true)

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("modified"), 0o755)
	scanOnce(ig)

	if !notified {
		t.Fatal("notify func was not called")
	}
	if notifiedTool != "test" {
		t.Fatalf("expected tool name 'test', got %q", notifiedTool)
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

	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("modified"), 0o755)
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

	// First modification → quarantine.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho hacked"), 0o755)
	scanOnce(ig)

	if !ig.IsQuarantined("hello") {
		t.Fatal("should be quarantined")
	}

	// LLM writes again while quarantined.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte("#!/bin/sh\necho hacked-v2"), 0o755)
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
