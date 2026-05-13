package wasm

import (
	"context"
	"errors"
	"testing"

	"github.com/alamparelli/alf/internal/capability/envelope"
)

// buildWASMWithImports emits a minimal valid WebAssembly module that
// imports one function per entry. Each entry is (module, field) — the
// function signature is fixed at () -> (). This is enough to exercise
// CheckImports, which only looks at the (module, name) of each import,
// not at the signature.
//
// Layout (little-endian leb128 lengths throughout):
//
//	magic + version (8 bytes)
//	type section (id=1):
//	  1 x func type ( () -> () )
//	import section (id=2):
//	  N x (mod, name, kind=func, typeidx=0)
func buildWASMWithImports(entries [][2]string) []byte {
	var out []byte

	// Magic + version.
	out = append(out, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)

	// Type section: id=1, size=4, count=1, func marker, 0 params, 0 results.
	out = append(out, 0x01, 0x04, 0x01, 0x60, 0x00, 0x00)

	// Import section: id=2, body is count + each entry.
	var body []byte
	body = append(body, byte(len(entries)))
	for _, e := range entries {
		mod, name := e[0], e[1]
		body = append(body, byte(len(mod)))
		body = append(body, []byte(mod)...)
		body = append(body, byte(len(name)))
		body = append(body, []byte(name)...)
		body = append(body, 0x00, 0x00) // kind=func, typeidx=0
	}
	out = append(out, 0x02, byte(len(body)))
	out = append(out, body...)

	return out
}

func TestCheckImports_HappyPath_FSReadAndWrite(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{hostModuleALF, fnAlfFSRead},
		{hostModuleALF, fnAlfFSWrite},
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{
		FS: envelope.FSBlock{
			Reads:  []envelope.FSPath{{Path: "data/"}},
			Writes: []envelope.FSPath{{Path: "out/"}},
		},
	}
	if err := CheckImports(cm, m); err != nil {
		t.Errorf("CheckImports: %v", err)
	}
}

func TestCheckImports_LyingManifest_ReadDeclaresWriteImported(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{hostModuleALF, fnAlfFSWrite},
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{
		FS: envelope.FSBlock{
			Reads: []envelope.FSPath{{Path: "data/"}},
			// no Writes declared
		},
	}
	err = CheckImports(cm, m)
	if !errors.Is(err, ErrLyingManifest) {
		t.Fatalf("want ErrLyingManifest, got %v", err)
	}
}

func TestCheckImports_UnknownALFFunctionRejected(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{hostModuleALF, "alf_http_get"}, // not in 0.8.0 ABI
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{}
	err = CheckImports(cm, m)
	if !errors.Is(err, ErrLyingManifest) {
		t.Fatalf("want ErrLyingManifest for unknown ABI fn, got %v", err)
	}
}

func TestCheckImports_UnknownModuleRejected(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{"malicious_host", "do_evil"},
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{}
	err = CheckImports(cm, m)
	if !errors.Is(err, ErrUnknownImportModule) {
		t.Fatalf("want ErrUnknownImportModule, got %v", err)
	}
}

func TestCheckImports_WASIAllowedUnconditionally(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{hostModuleWASI, "clock_time_get"},
		{hostModuleWASI, "fd_write"},
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	// Empty manifest: WASI must still be allowed.
	m := &envelope.Manifest{}
	if err := CheckImports(cm, m); err != nil {
		t.Errorf("CheckImports with only WASI imports: %v", err)
	}
}

func TestCheckImports_NoImportsAccepted(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	// Minimal module with no imports.
	bin := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{}
	if err := CheckImports(cm, m); err != nil {
		t.Errorf("CheckImports on module with no imports: %v", err)
	}
}

// TestCheckImports_HTTPRequest_HappyPath pins #421 Wave 2: a guest
// that imports alf_http_request is accepted iff its manifest declares
// at least one [[http.scopes]] entry.
func TestCheckImports_HTTPRequest_HappyPath(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{hostModuleALF, fnAlfHTTPRequest},
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{
		HTTP: envelope.HTTPBlock{
			Scopes: []envelope.HTTPScope{{Host: "openlibrary.org"}},
		},
	}
	if err := CheckImports(cm, m); err != nil {
		t.Errorf("CheckImports: %v", err)
	}
}

// TestCheckImports_HTTPRequest_RejectedWithoutScopes pins the inverse:
// a guest imports alf_http_request but the manifest has no http.scopes
// → ErrLyingManifest. This is the gate that catches a manifest-vs-
// guest mismatch; without it, an unauthorised guest could still link
// the host function because BuildHostModule always exports it.
func TestCheckImports_HTTPRequest_RejectedWithoutScopes(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	bin := buildWASMWithImports([][2]string{
		{hostModuleALF, fnAlfHTTPRequest},
	})
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := &envelope.Manifest{} // empty — no http.scopes declared
	err = CheckImports(cm, m)
	if !errors.Is(err, ErrLyingManifest) {
		t.Errorf("got %v, want ErrLyingManifest", err)
	}
}

func TestCheckImports_NilCompiledModule(t *testing.T) {
	err := CheckImports(nil, &envelope.Manifest{})
	if err == nil {
		t.Fatal("want error on nil CompiledModule, got nil")
	}
}

func TestCheckImports_NilManifest(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	bin := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	cm, err := e.Compile(context.Background(), bin)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if err := CheckImports(cm, nil); err == nil {
		t.Fatal("want error on nil Manifest, got nil")
	}
}

