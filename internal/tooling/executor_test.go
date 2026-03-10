package tooling

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestExecutor_ToolNotFound(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tools.d"), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_1",
		Name:      "nonexistent",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("expected error for missing tool")
	}
	if result.ID != "call_1" {
		t.Errorf("expected ID 'call_1', got %q", result.ID)
	}
}

func TestExecutor_RunsTool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	// Create a simple echo tool that reads stdin and outputs it.
	script := "#!/bin/sh\ncat\n"
	toolPath := filepath.Join(toolsD, "echo-tool")
	os.WriteFile(toolPath, []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_2",
		Name:      "echo-tool",
		Arguments: `{"query": "test"}`,
	})

	if result.IsError {
		t.Errorf("unexpected error: %s", result.Output)
	}
	if result.Output != `{"query": "test"}` {
		t.Errorf("expected stdin piped to stdout, got %q", result.Output)
	}
}

func TestExecutor_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	script := "#!/bin/sh\nsleep 30\n"
	toolPath := filepath.Join(toolsD, "slow-tool")
	os.WriteFile(toolPath, []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 100 * time.Millisecond,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_3",
		Name:      "slow-tool",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("expected timeout error")
	}
}

func TestExecutor_NonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	toolsD := filepath.Join(dir, "tools.d")
	os.MkdirAll(toolsD, 0o755)

	script := "#!/bin/sh\necho 'something went wrong' >&2\nexit 1\n"
	toolPath := filepath.Join(toolsD, "fail-tool")
	os.WriteFile(toolPath, []byte(script), 0o755)

	e := &Executor{
		DataDir: dir,
		HomeDir: dir,
		Timeout: 5 * time.Second,
	}

	result := e.Execute(context.Background(), CallRequest{
		ID:        "call_4",
		Name:      "fail-tool",
		Arguments: "{}",
	})

	if !result.IsError {
		t.Error("expected error for non-zero exit")
	}
}
