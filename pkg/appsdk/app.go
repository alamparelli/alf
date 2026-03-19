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

// App is the top-level container for an ALF app.
type App struct {
	Name    string
	DataDir string
	Actions map[string]ActionFunc
}

// New creates an App with the given name. DataDir is read from ALF_APP_DATA_DIR.
func New(name string) *App {
	return &App{
		Name:    name,
		DataDir: os.Getenv("ALF_APP_DATA_DIR"),
		Actions: make(map[string]ActionFunc),
	}
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
