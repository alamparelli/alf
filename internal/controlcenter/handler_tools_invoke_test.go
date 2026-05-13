package controlcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/tooling"
)

// invokeStubCapability is the minimum capability.Capability the
// handler tests need — they exercise the /api/tools/invoke wire
// format and verify the Executor passes through, not the WASM
// runtime itself.
type invokeStubCapability struct {
	id  capability.ID
	out capability.Output
	err error
}

func (s *invokeStubCapability) Manifest() capability.Manifest {
	return capability.Manifest{ID: s.id, Kind: capability.KindTool, Name: string(s.id)}
}
func (s *invokeStubCapability) Permissions() capability.PermissionSet {
	return capability.PermissionSet{}
}
func (s *invokeStubCapability) Execute(_ context.Context, in capability.Input) (capability.Output, error) {
	return s.out, s.err
}

func newInvokeHandler(t *testing.T, cap capability.Capability) (*ToolInvokeHandler, *tooling.Registry) {
	t.Helper()
	capReg := capability.NewRegistry()
	if cap != nil {
		if err := capReg.Register(cap); err != nil {
			t.Fatalf("register capability: %v", err)
		}
	}
	reg := tooling.NewRegistry(t.TempDir())
	reg.SetCapabilityRegistry(capReg)
	if cap != nil {
		reg.RegisterWasmTool(tooling.ToolSchema{Name: string(cap.Manifest().ID), Description: "x"}, cap)
	}
	exec := &tooling.Executor{
		DataDir:  t.TempDir(),
		HomeDir:  t.TempDir(),
		Registry: reg,
		Timeout:  5 * time.Second,
	}
	return &ToolInvokeHandler{Executor: exec}, reg
}

// TestToolInvokeHandler_DispatchesToCapability is the headline wire test:
// POSTing {"name":"http-hello","arguments":"{...}"} reaches the
// underlying capability via Executor → Registry.CapabilityRegistry().
// The response carries the marshalled Output.Data so the wasm-tool
// CLI can print it verbatim to stdout.
func TestToolInvokeHandler_DispatchesToCapability(t *testing.T) {
	cap := &invokeStubCapability{
		id:  "http-hello",
		out: capability.Output{Data: map[string]any{"status": 200, "body": "ok"}},
	}
	h, _ := newInvokeHandler(t, cap)

	body, _ := json.Marshal(ToolInvokeRequest{
		Name:      "http-hello",
		Arguments: `{"url":"https://httpbin.org/get"}`,
		CallID:    "call_42",
	})
	req := httptest.NewRequest("POST", "/api/tools/invoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ToolInvokeResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.IsError {
		t.Errorf("expected success; got is_error=true error_message=%q", resp.ErrorMessage)
	}
	// Output is the JSON-encoded Output.Data — assert it round-trips.
	var data map[string]any
	if err := json.Unmarshal([]byte(resp.Output), &data); err != nil {
		t.Fatalf("Output is not valid JSON: %v (output=%q)", err, resp.Output)
	}
	if data["status"] != float64(200) {
		t.Errorf("status=%v, want 200", data["status"])
	}
}

// TestToolInvokeHandler_DesanitizesHyphens pins that the LLM-visible
// sanitised name (http_hello) reaches the same dispatcher as the
// direct CLI invocation (http-hello). Without this guarantee the
// agentic API tier and the CLI tier would behave differently — only
// one would resolve through the executor's hyphen-restoration step.
func TestToolInvokeHandler_DesanitizesHyphens(t *testing.T) {
	cap := &invokeStubCapability{
		id:  "http-hello",
		out: capability.Output{Data: "hello"},
	}
	h, _ := newInvokeHandler(t, cap)

	body, _ := json.Marshal(ToolInvokeRequest{Name: "http_hello", Arguments: "{}"})
	req := httptest.NewRequest("POST", "/api/tools/invoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var resp ToolInvokeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.IsError {
		t.Errorf("desanitised name should dispatch successfully; is_error=true msg=%q", resp.ErrorMessage)
	}
	if resp.Output != "hello" {
		t.Errorf("Output=%q, want %q", resp.Output, "hello")
	}
}

// TestToolInvokeHandler_RejectsEmptyName guards against an empty
// dispatch — the Executor itself would reject too, but surfacing it
// at the HTTP boundary keeps the CLI error path predictable
// (exit-code → "invalid request" instead of a 500-ish "not found").
func TestToolInvokeHandler_RejectsEmptyName(t *testing.T) {
	h, _ := newInvokeHandler(t, nil)

	body := []byte(`{"name":"   ","arguments":"{}"}`)
	req := httptest.NewRequest("POST", "/api/tools/invoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "name is required") {
		t.Errorf("missing 'name is required' in error body: %s", rec.Body.String())
	}
}

// TestToolInvokeHandler_DefaultsEmptyArguments pins the empty-args
// shortcut: tools with no inputs (a schema that allows zero
// properties) should not need the CLI to spell out `{}` on the wire.
// The handler defaults Arguments to "{}" so jsonToCLI / capability
// Input both see well-formed JSON.
func TestToolInvokeHandler_DefaultsEmptyArguments(t *testing.T) {
	cap := &invokeStubCapability{id: "noargs", out: capability.Output{Data: "ok"}}
	h, _ := newInvokeHandler(t, cap)

	body := []byte(`{"name":"noargs"}`)
	req := httptest.NewRequest("POST", "/api/tools/invoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp ToolInvokeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Output != "ok" {
		t.Errorf("Output=%q, want %q", resp.Output, "ok")
	}
}

// TestToolInvokeHandler_ToolErrorSurfaces pins that a capability
// returning a non-nil error bubbles up as is_error=true in the
// response — the wasm-tool CLI uses this to choose its own exit code,
// so a silent "all good" on a failing tool would hide bugs.
func TestToolInvokeHandler_ToolErrorSurfaces(t *testing.T) {
	cap := &invokeStubCapability{id: "boom", err: errors.New("kaboom")}
	h, _ := newInvokeHandler(t, cap)

	body, _ := json.Marshal(ToolInvokeRequest{Name: "boom", Arguments: "{}"})
	req := httptest.NewRequest("POST", "/api/tools/invoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var resp ToolInvokeResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.IsError {
		t.Fatal("tool error should surface as is_error=true")
	}
	if !strings.Contains(resp.ErrorMessage, "kaboom") {
		t.Errorf("ErrorMessage should mention underlying error; got %q", resp.ErrorMessage)
	}
}

// TestToolInvokeHandler_MissingExecutor pins the degraded-boot path:
// if the daemon comes up without an Executor (test fixture, partial
// init), the handler reports 503 rather than panicking on a nil
// receiver. Same shape as similar checks elsewhere in factory.go.
func TestToolInvokeHandler_MissingExecutor(t *testing.T) {
	h := &ToolInvokeHandler{Executor: nil}
	body, _ := json.Marshal(ToolInvokeRequest{Name: "x"})
	req := httptest.NewRequest("POST", "/api/tools/invoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status=%d, want 503", rec.Code)
	}
}

// TestToolInvokeHandler_RejectsGet pins method-not-allowed for
// non-POST. The tools.sock is GET-friendly for the read-oriented
// endpoints elsewhere; tool invocation is intentionally write-shaped.
func TestToolInvokeHandler_RejectsGet(t *testing.T) {
	h, _ := newInvokeHandler(t, nil)
	req := httptest.NewRequest("GET", "/api/tools/invoke", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", rec.Code)
	}
}
