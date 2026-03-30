// Package appsdk provides a lightweight SDK for building ALF apps.
// Apps receive JSON input via stdin and dispatch to registered actions.
package appsdk

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ActionFunc is the handler signature for an app action.
type ActionFunc func(ctx *Context) error

// Context carries the dispatch context for an action invocation.
type Context struct {
	Action  string
	Args    map[string]any
	DataDir string
}

// String returns the string value for key, or empty string if missing or wrong type.
func (c *Context) String(key string) string {
	v, ok := c.Args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// Int returns the integer value for key, or def if missing or not convertible.
func (c *Context) Int(key string, def int) int {
	v, ok := c.Args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			return def
		}
		return i
	default:
		return def
	}
}

// Bool returns the boolean value for key, or def if missing or not convertible.
// Handles bool, string ("true"/"false"/"1"/"0"), and float64 (0=false, non-zero=true).
func (c *Context) Bool(key string, def bool) bool {
	v, ok := c.Args[key]
	if !ok {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(b) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		default:
			return def
		}
	case float64:
		return b != 0
	default:
		return def
	}
}

// Float64 returns the float64 value for key, or def if missing or not convertible.
func (c *Context) Float64(key string, def float64) float64 {
	v, ok := c.Args[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return n
	case string:
		f, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return def
		}
		return f
	default:
		return def
	}
}

// StringSlice returns a string slice for key. Returns nil if missing or not a slice.
func (c *Context) StringSlice(key string) []string {
	v, ok := c.Args[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// App is the top-level container for an ALF app.
type App struct {
	Name    string
	DataDir string
	Actions map[string]ActionFunc
	vault   *VaultClient
}

// New creates an App with the given name. DataDir is read from ALF_APP_DATA_DIR.
func New(name string) *App {
	return &App{
		Name:    name,
		DataDir: os.Getenv("ALF_APP_DATA_DIR"),
		Actions: make(map[string]ActionFunc),
	}
}

// Vault returns a client for the vault proxy socket. Lazy-initialized.
// Returns nil if VAULT_PROXY_SOCK is not set (app has no vault services).
func (a *App) Vault() *VaultClient {
	if a.vault == nil {
		a.vault, _ = NewVaultClient()
	}
	return a.vault
}

// Action registers a named action handler.
func (a *App) Action(name string, fn ActionFunc) {
	a.Actions[name] = fn
}

// Run parses stdin as JSON, resolves the target action, and dispatches.
//
// Action resolution order:
//  1. Binary name: take filepath.Base(os.Args[0]), find first '-', use everything after it.
//  2. Fallback: read the "action" field from the JSON input.
func (a *App) Run() {
	// Handle --help for ALF toolbox discovery.
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		actions := make([]string, 0, len(a.Actions))
		for name := range a.Actions {
			actions = append(actions, name)
		}
		fmt.Fprintf(os.Stdout, "%s — ALF marketplace app\n\nActions: %s\n\nInput: JSON on stdin with \"action\" field.\n",
			a.Name, strings.Join(actions, ", "))
		os.Exit(0)
	}

	// Determine action from binary name.
	action := actionFromBinary(os.Args[0])

	// Read stdin JSON.
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		Fail(fmt.Sprintf("failed to read stdin: %v", err))
	}

	var raw map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &raw); err != nil {
			Fail(fmt.Sprintf("invalid JSON input: %v", err))
		}
	}
	if raw == nil {
		raw = make(map[string]any)
	}

	// Fallback: action from JSON field.
	if action == "" {
		if v, ok := raw["action"].(string); ok {
			action = v
		}
	}

	if action == "" {
		Fail("no action resolved from binary name or input")
	}

	fn, ok := a.Actions[action]
	if !ok {
		Fail(fmt.Sprintf("unknown action: %s", action))
	}

	ctx := &Context{
		Action:  action,
		Args:    raw,
		DataDir: a.DataDir,
	}

	if err := fn(ctx); err != nil {
		Fail(err.Error())
	}
}

// actionFromBinary extracts the action from the binary name.
// Given "appname-dosomething" it returns "dosomething".
// Returns empty string if no '-' is found.
func actionFromBinary(arg0 string) string {
	base := filepath.Base(arg0)
	idx := strings.Index(base, "-")
	if idx < 0 || idx == len(base)-1 {
		return ""
	}
	return base[idx+1:]
}

// Respond writes a plain text response to stdout.
func Respond(text string) {
	fmt.Fprint(os.Stdout, text)
}

// RespondJSON marshals v as JSON and writes it to stdout.
func RespondJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(v); err != nil {
		Fail(fmt.Sprintf("failed to encode JSON response: %v", err))
	}
}

// Fail writes msg to stderr and exits with code 1.
func Fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
