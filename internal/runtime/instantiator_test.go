package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/capability/handle"
)

// resetMint wraps handle.ResetMintForTesting for readability and to
// keep a single chokepoint if the reset API ever grows.
func resetMint(t *testing.T) { t.Helper(); handle.ResetMintForTesting() }

func TestInstantiator_EmptyManifestRejected(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	_, err := inst.Instantiate(context.Background(), SignedManifest{})
	if !errors.Is(err, ErrManifestID) {
		t.Fatalf("want ErrManifestID for empty Manifest.ID, got %v", err)
	}
}

func TestInstantiator_HappyPath_NoPermissions(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{ID: "cap-empty", Kind: capability.KindTool},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer handleInst.Close()

	if handleInst.Owner != "cap-empty" {
		t.Errorf("Owner=%q, want cap-empty", handleInst.Owner)
	}
	// No Permissions declared → every handle slot nil.
	if handleInst.FS != nil || handleInst.HTTP != nil || handleInst.Exec != nil ||
		handleInst.Secrets != nil || handleInst.Tool != nil {
		t.Errorf("empty manifest produced non-nil handles: FS=%v HTTP=%v Exec=%v Secrets=%v Tool=%v",
			handleInst.FS, handleInst.HTTP, handleInst.Exec, handleInst.Secrets, handleInst.Tool)
	}
}

func TestInstantiator_ForgesFSFromFilePaths(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{
			ID: "cap-fs",
			Permissions: capability.PermissionSet{
				FilePaths: []string{"data/"},
			},
		},
		BaseDir: "/tmp/cap-fs",
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer handleInst.Close()

	if handleInst.FS == nil {
		t.Fatal("FS handle nil despite declared FilePaths")
	}
	// Other slots stay empty.
	if handleInst.HTTP != nil || handleInst.Exec != nil || handleInst.Secrets != nil || handleInst.Tool != nil {
		t.Errorf("non-FS handles should be nil")
	}
}

func TestInstantiator_ForgesHTTPFromNetworks(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{
			ID: "cap-http",
			Permissions: capability.PermissionSet{
				Networks: []string{"api.example.com", "*.github.com"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer handleInst.Close()

	if handleInst.HTTP == nil {
		t.Fatal("HTTP handle nil despite declared Networks")
	}
	if handleInst.FS != nil {
		t.Error("FS handle should be nil when no FilePaths declared")
	}
}

func TestInstantiator_ForgesSecretsFromKeyPatterns(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{
			ID: "cap-secrets",
			Permissions: capability.PermissionSet{
				Secrets: []string{"github_*"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer handleInst.Close()

	if handleInst.Secrets == nil {
		t.Fatal("Secrets handle nil despite declared Secrets")
	}

	// Nil reader yields ErrSecretNotFound on every lookup — defensive
	// default until Manager is wired. Verifies the forge chain rather
	// than leaving Secrets as "forged and silently broken".
	_, err = handleInst.Secrets.Get(context.Background(), "github_token")
	if !errors.Is(err, handle.ErrSecretNotFound) {
		t.Errorf("nil reader should return ErrSecretNotFound, got %v", err)
	}
}

func TestInstantiator_ForgesAllThreeTogether(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{
			ID: "cap-full",
			Permissions: capability.PermissionSet{
				FilePaths: []string{"data/"},
				Networks:  []string{"api.example.com"},
				Secrets:   []string{"api_key"},
			},
		},
		BaseDir: "/tmp/cap-full",
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	defer handleInst.Close()

	if handleInst.FS == nil || handleInst.HTTP == nil || handleInst.Secrets == nil {
		t.Errorf("expected FS+HTTP+Secrets forged, got FS=%v HTTP=%v Secrets=%v",
			handleInst.FS != nil, handleInst.HTTP != nil, handleInst.Secrets != nil)
	}
	if handleInst.Exec != nil || handleInst.Tool != nil {
		t.Error("Exec + Tool should be nil — no manifest fields map to them today")
	}
}

// failingVerifier rejects every manifest, proving the verifier slot
// actually gates the forge.
type failingVerifier struct{}

func (failingVerifier) Verify(_ SignedManifest) error {
	return errors.New("signature rejected")
}

func TestInstantiator_TrustVerifierCanReject(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator(WithTrustVerifier(failingVerifier{}))

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{ID: "cap"},
	})
	if err == nil {
		t.Fatal("expected verifier error, got nil")
	}
	if handleInst != nil {
		t.Error("verifier rejected — no Instance must be returned")
	}
}

func TestInstantiator_CloseRevokesAllHandles(t *testing.T) {
	resetMint(t)
	inst := NewInstantiator()

	handleInst, err := inst.Instantiate(context.Background(), SignedManifest{
		Manifest: capability.Manifest{
			ID: "cap-revoke",
			Permissions: capability.PermissionSet{
				FilePaths: []string{"data/"},
				Networks:  []string{"api.example.com"},
				Secrets:   []string{"k"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	handleInst.Close()

	// Every forged handle must surface ErrRevoked after Close.
	if _, err := handleInst.FS.Read(context.Background(), "anything"); !errors.Is(err, handle.ErrRevoked) {
		t.Errorf("FS.Read after Close: want ErrRevoked, got %v", err)
	}
	if _, err := handleInst.Secrets.Get(context.Background(), "k"); !errors.Is(err, handle.ErrRevoked) {
		t.Errorf("Secrets.Get after Close: want ErrRevoked, got %v", err)
	}
	// HTTP revocation is checked via the revoked flag on a Do call —
	// exercised in the handle package's own tests; here we assert the
	// flag flipped by probing the lifecycle context.
	if handleInst.Context().Err() == nil {
		t.Error("Instance lifecycle context was not cancelled by Close")
	}
}

func TestInstantiator_SecondMintPanics(t *testing.T) {
	resetMint(t)
	_ = NewInstantiator()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("second NewInstantiator should have panicked via MintRuntimeToken")
		}
	}()
	_ = NewInstantiator()
}
