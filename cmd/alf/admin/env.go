package admin

import (
	"io"
	"path/filepath"
	"time"

	"github.com/alamparelli/alf/internal/admin/userkey"
	"github.com/alamparelli/alf/internal/cli"
)

// Env carries the dependencies every admin subcommand consumes. The
// TTY gate, the timestamp source, and the on-disk resource paths
// live here so production wiring is one place and every handler is
// fully injectable for tests.
//
// Field choices are deliberate: there is no http.Client, no daemon
// socket, no tool registry. The §6 admin trust domain mutates files
// directly; if a future handler needs network or runtime access,
// adding it should be reviewed as carefully as a new archtest
// allowlist entry.
type Env struct {
	// TrustDir is <dataDir>/trust/ — the operator-managed trust store
	// the alf trust subcommands mutate.
	TrustDir string

	// UserKeyPath is the absolute path to the §7.3 Tier-3
	// user-endorsed key record (see internal/admin/userkey).
	UserKeyPath string

	// I/O handles. Tests substitute these with bytes.Buffer-backed
	// readers/writers so subcommands run end-to-end without touching
	// the real os.Std* streams.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// IsTerminal returns true when Stdin is a TTY. The mutating
	// commands refuse with ErrNonInteractive when it returns false —
	// non-TTY input is the prompt-injection signature this boundary
	// exists to block.
	IsTerminal func() bool

	// Now is injected so tests can pin the signed-at timestamp and
	// the default revoke instant. Production wires it to time.Now.
	Now func() time.Time

	// ReadPassword reads a passphrase from Stdin without echo on a
	// real TTY. Production wires an x/term-backed reader; tests
	// substitute a function that pulls bytes from a pre-seeded
	// buffer. The prompt is printed to Stdout before the read so
	// callers control the prompt text directly.
	ReadPassword func(prompt string) ([]byte, error)
}

// TrustEnv is the legacy name retained for the existing trust_test.go
// fixtures. New code should use Env directly. The alias will be
// removed once the test suite is rebased onto Env (no behavioural
// change — the underlying type is identical).
type TrustEnv = Env

// DefaultTrustDir resolves the on-disk trust directory from the alf
// install layout: <install>/data/trust/. Cmd/alf/main calls this to
// build the production Env; tests pass a tmpdir directly.
func DefaultTrustDir() string {
	return filepath.Join(cli.AlfDir(), "data", "trust")
}

// DefaultUserKeyPath resolves the path to the user-endorsed key file
// from the alf install layout: <install>/data/keys/user-endorsed.json.
// Sibling to the daemon-bootstrapped key under <install>/data/keys/.
func DefaultUserKeyPath() string {
	return userkey.DefaultPath(filepath.Join(cli.AlfDir(), "data"))
}
