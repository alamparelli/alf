package wasm

import (
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// ErrLyingManifest is returned when a compiled guest module imports a
// host symbol the manifest did not declare. Per ARCHITECTURE-SECURITY
// §4.2 handle hygiene invariant #3 (WASM import cross-check), this is
// a hard failure: the bundle and its signed manifest disagree about
// what authority the guest will exercise, and the signed manifest wins.
var ErrLyingManifest = errors.New("wasm: guest imports a symbol not in declared manifest scope")

// ErrUnknownImportModule is returned when the guest imports from a
// module other than "alf" (the host ABI) or "wasi_snapshot_preview1"
// (Go runtime init). Unknown modules mean an unauthorised embedding
// path — reject before instantiation so the guest never runs.
var ErrUnknownImportModule = errors.New("wasm: guest imports from unknown module")

// Module + name constants for the 0.8.0 host ABI (alf-fs-v0). See
// docs/WASM.md §3 for the full ABI reference.
const (
	hostModuleALF  = "alf"
	hostModuleWASI = "wasi_snapshot_preview1"

	fnAlfFSRead  = "alf_fs_read"
	fnAlfFSWrite = "alf_fs_write"
)

// CheckImports enforces handle hygiene invariant #3: every host symbol
// the guest imports must be authorised by the manifest. Specifically:
//
//   - alf.alf_fs_read is allowed iff manifest.FS.Reads is non-empty
//   - alf.alf_fs_write is allowed iff manifest.FS.Writes is non-empty
//   - any other name in module "alf" is rejected
//   - module wasi_snapshot_preview1 is allowed unconditionally (the Go
//     runtime needs it for clock / random / args / fd_write for panic
//     messages — WASI cannot touch the host filesystem ambiently
//     because no pre-opens are configured, see WASM.md §3.1)
//   - any other import module is rejected outright
//
// This runs AFTER the manifest has been verified (envelope.Verify) and
// AFTER the guest bytes have been compiled (wazero.Runtime.CompileModule),
// but BEFORE instantiation. A lying bundle therefore never gets to
// execute — import resolution never happens for rejected modules.
//
// Returns ErrLyingManifest wrapped with the offending (module, name)
// when the manifest does not authorise an imported symbol, and
// ErrUnknownImportModule for non-alf / non-wasi modules. The wrapped
// error is the single source the audit log consumes.
func CheckImports(cm wazero.CompiledModule, m *envelope.Manifest) error {
	if cm == nil {
		return fmt.Errorf("wasm: CheckImports called with nil CompiledModule")
	}
	if m == nil {
		return fmt.Errorf("wasm: CheckImports called with nil Manifest")
	}

	allowFSRead := len(m.FS.Reads) > 0
	allowFSWrite := len(m.FS.Writes) > 0

	for _, def := range cm.ImportedFunctions() {
		mod, name, _ := def.Import()
		switch mod {
		case hostModuleWASI:
			// Unconditionally allowed — Go runtime init. No host-
			// filesystem pre-opens are configured at instantiate
			// time, so wasi_snapshot_preview1 cannot reach the
			// host FS ambiently.
			continue
		case hostModuleALF:
			switch name {
			case fnAlfFSRead:
				if !allowFSRead {
					return fmt.Errorf("%w: imports %s.%s but manifest declares no fs.reads", ErrLyingManifest, mod, name)
				}
			case fnAlfFSWrite:
				if !allowFSWrite {
					return fmt.Errorf("%w: imports %s.%s but manifest declares no fs.writes", ErrLyingManifest, mod, name)
				}
			default:
				return fmt.Errorf("%w: imports %s.%s — not in the 0.8.0 host ABI", ErrLyingManifest, mod, name)
			}
		default:
			return fmt.Errorf("%w: imports %s.%s", ErrUnknownImportModule, mod, name)
		}
	}
	return nil
}
