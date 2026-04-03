package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderCompose is a test helper that renders the docker-compose template and returns its content.
func renderCompose(t *testing.T, data ComposeData) string {
	t.Helper()
	dir := t.TempDir()
	if err := RenderDockerCompose(dir, data); err != nil {
		t.Fatalf("RenderDockerCompose: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	return string(out)
}

// extractServiceBlock returns the lines belonging to a given service (e.g. "alf", "whisper").
func extractServiceBlock(yml, service string) []string {
	lines := strings.Split(yml, "\n")
	var block []string
	inService := false
	for _, line := range lines {
		// Service definitions are at 2-space indent.
		if strings.HasPrefix(line, "  "+service+":") {
			inService = true
			continue
		}
		if inService {
			// Next service or top-level key ends the block.
			if len(line) > 0 && line[0] != ' ' {
				break
			}
			if len(line) >= 3 && line[0] == ' ' && line[1] == ' ' && line[2] != ' ' {
				break
			}
			block = append(block, line)
		}
	}
	return block
}

func defaultComposeData() ComposeData {
	return ComposeData{
		Image:         "ghcr.io/alamparelli/alf:latest",
		CCPort:        "8080",
		CCExternalURL: "http://localhost:8080",
	}
}

// TestCompose_NoDockerSecretsBlock ensures the top-level "secrets:" Docker Compose
// block is absent — we no longer use the Docker secrets mechanism.
func TestCompose_NoDockerSecretsBlock(t *testing.T) {
	yml := renderCompose(t, defaultComposeData())

	// Count occurrences of "secrets:" at the start of a line (top-level block).
	// Service-level "secrets:" would be indented, but we removed those too.
	for _, line := range strings.Split(yml, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "secrets:" {
			t.Errorf("found 'secrets:' block in compose output — Docker secrets should be removed:\n%s", line)
		}
	}
}

// TestCompose_AlfServiceNoRunSecrets ensures the alf service has no /run/secrets references.
// Sidecars (whisper, embed) still use /run/secrets via direct bind mounts — that's expected.
func TestCompose_AlfServiceNoRunSecrets(t *testing.T) {
	yml := renderCompose(t, defaultComposeData())
	alfBlock := strings.Join(extractServiceBlock(yml, "alf"), "\n")

	if strings.Contains(alfBlock, "/run/secrets") {
		t.Error("alf service references /run/secrets — secrets should use vault-data via entrypoint")
	}
}

// TestCompose_SecretsStagingMount ensures the alf service mounts ./secrets as
// a read-only staging volume for the entrypoint to import into vault-data.
func TestCompose_SecretsStagingMount(t *testing.T) {
	yml := renderCompose(t, defaultComposeData())

	if !strings.Contains(yml, "./secrets:/opt/alf/secrets-staging:ro") {
		t.Error("compose output missing secrets-staging bind mount (./secrets:/opt/alf/secrets-staging:ro)")
	}
}

// TestCompose_SidecarSecretsBindMount ensures whisper and embed services still
// mount their individual secret files directly (they can't use vault-data).
func TestCompose_SidecarSecretsBindMount(t *testing.T) {
	yml := renderCompose(t, defaultComposeData())

	if !strings.Contains(yml, "./secrets/whisper_shared_secret:/run/secrets/whisper_shared_secret:ro") {
		t.Error("whisper service missing direct secret bind mount")
	}
	if !strings.Contains(yml, "./secrets/embed_shared_secret:/run/secrets/embed_shared_secret:ro") {
		t.Error("embed service missing direct secret bind mount")
	}
}

// TestCompose_NoSecretFileEnvInAlfService ensures the alf service does not
// export *_FILE environment variables — the entrypoint sets these dynamically.
func TestCompose_NoSecretFileEnvInAlfService(t *testing.T) {
	yml := renderCompose(t, defaultComposeData())
	alfBlock := strings.Join(extractServiceBlock(yml, "alf"), "\n")

	forbidden := []string{
		"CLAUDE_OAUTH_TOKEN_FILE",
		"WHISPER_SHARED_SECRET_FILE",
		"EMBED_SHARED_SECRET_FILE",
	}
	for _, env := range forbidden {
		if strings.Contains(alfBlock, env) {
			t.Errorf("alf service should not have %s env var — entrypoint sets it dynamically", env)
		}
	}
}

// TestCompose_HTTPS_NoDockerSecrets verifies the HTTPS variant also has no Docker secrets.
func TestCompose_HTTPS_NoDockerSecrets(t *testing.T) {
	data := defaultComposeData()
	data.EnableHTTPS = true
	data.Domain = "alf.example.com"
	data.AcmeEmail = "test@example.com"
	yml := renderCompose(t, data)
	alfBlock := strings.Join(extractServiceBlock(yml, "alf"), "\n")

	if strings.Contains(alfBlock, "/run/secrets") {
		t.Error("HTTPS alf service references /run/secrets")
	}
	for _, line := range strings.Split(yml, "\n") {
		if strings.TrimSpace(line) == "secrets:" {
			t.Error("HTTPS compose output has top-level 'secrets:' block")
		}
	}
}
