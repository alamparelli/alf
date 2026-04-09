package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the repo root by walking up from the test file location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine repo root")
	}
	// file is internal/cli/version_injection_test.go → walk up 3 levels
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func readRepoFile(t *testing.T, relPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), relPath))
	if err != nil {
		t.Fatalf("cannot read %s: %v", relPath, err)
	}
	return string(data)
}

// TestVersionInjection_Dockerfile ensures the Dockerfile declares BUILD_VERSION
// and injects it into alf-daemon via ldflags.
func TestVersionInjection_Dockerfile(t *testing.T) {
	content := readRepoFile(t, "Dockerfile")
	if !strings.Contains(content, "ARG BUILD_VERSION") {
		t.Error("Dockerfile missing ARG BUILD_VERSION")
	}
	if !strings.Contains(content, "X main.version=${BUILD_VERSION}") {
		t.Error("Dockerfile ldflags does not inject BUILD_VERSION into alf-daemon")
	}
}

// TestVersionInjection_CIWorkflow ensures the GitHub Actions release workflow
// passes BUILD_VERSION as a build-arg. This was the root cause of #236.
func TestVersionInjection_CIWorkflow(t *testing.T) {
	content := readRepoFile(t, ".github/workflows/release.yml")
	if !strings.Contains(content, "BUILD_VERSION") {
		t.Error("CI workflow missing BUILD_VERSION — daemon will report 'dev' in prod (#236)")
	}
}

// TestVersionInjection_ReleaseScript ensures release.sh passes BUILD_VERSION.
func TestVersionInjection_ReleaseScript(t *testing.T) {
	content := readRepoFile(t, "scripts/release.sh")
	if !strings.Contains(content, "BUILD_VERSION") {
		t.Error("release.sh missing BUILD_VERSION")
	}
}

// TestVersionInjection_DevDeploy ensures dev-deploy.sh passes BUILD_VERSION.
func TestVersionInjection_DevDeploy(t *testing.T) {
	content := readRepoFile(t, "scripts/dev-deploy.sh")
	if !strings.Contains(content, "BUILD_VERSION") {
		t.Error("dev-deploy.sh missing BUILD_VERSION")
	}
}

// TestVersionInjection_DevLocal ensures dev-local.sh passes BUILD_VERSION.
func TestVersionInjection_DevLocal(t *testing.T) {
	content := readRepoFile(t, "scripts/dev-local.sh")
	if !strings.Contains(content, "BUILD_VERSION") {
		t.Error("dev-local.sh missing BUILD_VERSION")
	}
}
