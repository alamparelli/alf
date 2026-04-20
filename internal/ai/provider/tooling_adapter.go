package provider

import (
	"context"

	"github.com/alamparelli/alf/internal/tooling"
)

// ToolingExecutorAdapter wraps a tooling.Executor to implement provider.ToolExecutor.
// Use this to connect the unified tool registry to a ToolLoop.
type ToolingExecutorAdapter struct {
	exec *tooling.Executor
}

// NewToolingExecutorAdapter creates an adapter from a tooling.Executor.
func NewToolingExecutorAdapter(exec *tooling.Executor) *ToolingExecutorAdapter {
	return &ToolingExecutorAdapter{exec: exec}
}

// Execute implements provider.ToolExecutor.
func (a *ToolingExecutorAdapter) Execute(ctx context.Context, call ToolCallRequest) ToolCallResult {
	r := a.exec.Execute(ctx, tooling.CallRequest{
		ID:        call.ID,
		Name:      call.Name,
		Arguments: call.Arguments,
	})
	return ToolCallResult{
		ID:           r.ID,
		Output:       r.Output,
		IsError:      r.IsError,
		ExitCode:     r.ExitCode,
		ErrorMessage: r.ErrorMessage,
	}
}
