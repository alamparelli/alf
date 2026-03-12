package tooling

import "context"

// NativeTool is a Go-native tool implementation for API LLMs.
// Native tools take priority over subprocess tools in Executor.
type NativeTool interface {
	ToolName() string
	Schema() ToolSchema
	Run(ctx context.Context, argsJSON string) (string, error)
}
