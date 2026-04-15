package tooling

import (
	"context"
	"encoding/json"
	"fmt"
)

// NativeTool is a Go-native tool implementation for API LLMs.
// Native tools take priority over subprocess tools in Executor.
type NativeTool interface {
	ToolName() string
	Schema() ToolSchema
	Run(ctx context.Context, argsJSON string) (string, error)
}

// parseArgs decodes argsJSON into out, wrapping decode errors with the
// standard "invalid arguments" prefix used by every native tool.
func parseArgs(argsJSON string, out any) error {
	if err := json.Unmarshal([]byte(argsJSON), out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
