package appsdk

import (
	"os"
	"testing"
)

func TestContext_String(t *testing.T) {
	ctx := &Context{Args: map[string]any{
		"name": "alf",
		"age":  42,
	}}
	if got := ctx.String("name"); got != "alf" {
		t.Errorf("expected 'alf', got %q", got)
	}
	if got := ctx.String("age"); got != "" {
		t.Errorf("non-string must return empty, got %q", got)
	}
	if got := ctx.String("missing"); got != "" {
		t.Errorf("missing key must return empty, got %q", got)
	}
}

func TestContext_Int(t *testing.T) {
	ctx := &Context{Args: map[string]any{
		"n":     float64(7),
		"s":     "42",
		"bogus": "not-a-number",
		"typ":   true,
	}}
	if got := ctx.Int("n", 99); got != 7 {
		t.Errorf("float64 path: expected 7, got %d", got)
	}
	if got := ctx.Int("s", 99); got != 42 {
		t.Errorf("string path: expected 42, got %d", got)
	}
	if got := ctx.Int("bogus", 99); got != 99 {
		t.Errorf("unparseable string must return default, got %d", got)
	}
	if got := ctx.Int("typ", 99); got != 99 {
		t.Errorf("unsupported type must return default, got %d", got)
	}
	if got := ctx.Int("missing", 99); got != 99 {
		t.Errorf("missing key must return default, got %d", got)
	}
}

func TestNew_UsesEnvDataDir(t *testing.T) {
	t.Setenv("ALF_APP_DATA_DIR", "/data/myapp")
	a := New("myapp")
	if a.Name != "myapp" {
		t.Errorf("Name mismatch: %q", a.Name)
	}
	if a.DataDir != "/data/myapp" {
		t.Errorf("DataDir mismatch: %q", a.DataDir)
	}
	if a.Actions == nil {
		t.Error("Actions map must be initialised")
	}
}

func TestApp_Action_RegistersHandler(t *testing.T) {
	a := New("x")
	a.Action("greet", func(*Context) error { return nil })
	if _, ok := a.Actions["greet"]; !ok {
		t.Error("action not registered")
	}
}

func TestActionFromBinary(t *testing.T) {
	tests := []struct {
		arg  string
		want string
	}{
		{"/usr/local/bin/myapp-dosomething", "dosomething"},
		{"myapp-run", "run"},
		{"/bin/myapp", ""},          // no dash
		{"/bin/myapp-", ""},         // dash at end
		{"/bin/a-b-c", "b-c"},       // first dash counts, rest is action
	}
	for _, tt := range tests {
		if got := actionFromBinary(tt.arg); got != tt.want {
			t.Errorf("actionFromBinary(%q) = %q, want %q", tt.arg, got, tt.want)
		}
	}
}

func TestApp_Vault_LazyInit_WithoutSocketReturnsNil(t *testing.T) {
	// Without VAULT_PROXY_SOCK, NewVaultClient fails → Vault() returns nil.
	os.Unsetenv("VAULT_PROXY_SOCK")
	a := New("x")
	// First call: attempts to init, stays nil because no socket.
	if got := a.Vault(); got != nil {
		t.Errorf("expected nil client without VAULT_PROXY_SOCK, got %v", got)
	}
	// Second call: cached nil, still returns nil without a panic.
	if got := a.Vault(); got != nil {
		t.Errorf("second call expected nil, got %v", got)
	}
}
