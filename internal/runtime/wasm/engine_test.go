package wasm

import (
	"context"
	"strings"
	"testing"
)

// minimalWasm is a valid empty WebAssembly module: 4-byte magic
// (\0asm) + 4-byte version (1). wazero accepts it as a well-formed
// module with zero sections; instantiating it is a no-op.
var minimalWasm = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func TestEngine_Compile_HappyPath(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	cm, err := e.Compile(context.Background(), minimalWasm)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if cm == nil {
		t.Fatal("CompiledModule nil on success")
	}
}

func TestEngine_Compile_RejectsEmpty(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	_, err := e.Compile(context.Background(), nil)
	if err == nil {
		t.Fatal("want error on empty bytes, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("err=%v, want mention of empty", err)
	}
}

func TestEngine_Compile_RejectsGarbage(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	garbage := []byte("this is not webassembly")
	_, err := e.Compile(context.Background(), garbage)
	if err == nil {
		t.Fatal("want error on garbage bytes, got nil")
	}
	if !strings.HasPrefix(err.Error(), "wasm: compile") {
		t.Errorf("err=%v, want wrapped compile error", err)
	}
}

func TestEngine_Close_Idempotent(t *testing.T) {
	e := NewEngine(context.Background())
	if err := e.Close(context.Background()); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := e.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestEngine_Close_NilReceiver(t *testing.T) {
	var e *Engine
	if err := e.Close(context.Background()); err != nil {
		t.Errorf("Close on nil engine: %v", err)
	}
}

func TestEngine_Runtime_ExposesNonNilRuntime(t *testing.T) {
	e := NewEngine(context.Background())
	t.Cleanup(func() { _ = e.Close(context.Background()) })

	if e.Runtime() == nil {
		t.Fatal("Runtime() returned nil")
	}
}
