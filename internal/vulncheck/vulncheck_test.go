package vulncheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoVulncheck runs govulncheck on the entire project.
// Skips if govulncheck is not installed.
func TestGoVulncheck(t *testing.T) {
	path, err := findGovulncheck()
	if err != nil {
		t.Skip("govulncheck not installed — run: go install golang.org/x/vuln/cmd/govulncheck@latest")
	}

	cmd := exec.Command(path, "./...")
	cmd.Dir = projectRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := string(out)
		// Stdlib-only vulnerabilities can't be fixed without upgrading Go.
		// Warn instead of failing; fail only for third-party vulns.
		hasThirdParty := false
		for _, line := range strings.Split(output, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Found in:") && !strings.Contains(trimmed, "@go") {
				hasThirdParty = true
				break
			}
		}
		if hasThirdParty {
			t.Fatalf("govulncheck found third-party vulnerabilities:\n%s", output)
		}
		t.Logf("govulncheck: stdlib-only vulnerabilities (upgrade Go to fix):\n%s", output)
		return
	}
	t.Logf("govulncheck: %s", strings.TrimSpace(string(out)))
}

// TestPipAudit runs pip-audit on installed Python packages.
// Skips if pip-audit is not installed (typical on local dev without venv).
func TestPipAudit(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}

	// Check if pip-audit is available as a module.
	check := exec.Command(python, "-m", "pip_audit", "--version")
	if err := check.Run(); err != nil {
		t.Skip("pip-audit not installed — run: pip3 install pip-audit")
	}

	// Write a temp requirements file scoped to ALF's Python deps only.
	// This avoids false positives from unrelated packages in the local env.
	reqFile := filepath.Join(t.TempDir(), "requirements.txt")
	alfPythonDeps := "faster-whisper\nsentence-transformers\n"
	if err := os.WriteFile(reqFile, []byte(alfPythonDeps), 0o644); err != nil {
		t.Fatalf("write requirements: %v", err)
	}

	cmd := exec.Command(python, "-m", "pip_audit", "--desc", "--requirement", reqFile)
	out, err := cmd.CombinedOutput()
	output := string(out)

	// pip-audit exits non-zero for both vulns and warnings (e.g. packages not on PyPI).
	// Only fail on actual vulnerability lines (format: "package  version  vuln-id  description").
	if err != nil {
		lines := strings.Split(output, "\n")
		hasVuln := false
		for _, line := range lines {
			// Vulnerability lines contain CVE/PYSEC/GHSA IDs.
			if strings.Contains(line, "PYSEC-") || strings.Contains(line, "CVE-") || strings.Contains(line, "GHSA-") {
				hasVuln = true
				break
			}
		}
		if hasVuln {
			t.Fatalf("pip-audit found vulnerabilities:\n%s", output)
		}
		// Warnings only (e.g. packages not on PyPI) — log but don't fail.
		t.Logf("pip-audit warnings (no CVEs):\n%s", output)
		return
	}
	t.Logf("pip-audit: clean (%d bytes output)", len(out))
}

func findGovulncheck() (string, error) {
	// Try PATH first.
	if path, err := exec.LookPath("govulncheck"); err == nil {
		return path, nil
	}
	// Try GOPATH/bin (common when not in shell PATH).
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, _ := os.UserHomeDir()
		gopath = filepath.Join(home, "go")
	}
	candidate := filepath.Join(gopath, "bin", "govulncheck")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", exec.ErrNotFound
}

func projectRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "env", "GOMOD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("cannot determine project root")
	}
	mod := strings.TrimSpace(string(out))
	// go.mod path → parent directory
	idx := strings.LastIndex(mod, "/")
	if idx < 0 {
		t.Fatal("unexpected GOMOD path")
	}
	return mod[:idx]
}
