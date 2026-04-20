package tooling

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/alamparelli/alf/internal/capability"
)

// nativeCapability adapts a NativeTool to the capability.Capability contract.
// It lives in tooling/ (not capability/) to keep the dependency edge
// capability ← tooling — capability must not import tooling.
//
// During Step 2 (#338 / C1) this adapter is dual-registered alongside the
// legacy NativeTool entry in tooling.Registry so consumers can migrate one
// at a time. The adapter disappears once KindTool natives live natively in
// capability/.
type nativeCapability struct {
	tool NativeTool
}

// asCapability wraps a NativeTool. It is package-internal because external
// code should consume capabilities via capability.Registry, not construct
// adapters directly.
func asCapability(t NativeTool) capability.Capability {
	return nativeCapability{tool: t}
}

func (n nativeCapability) Manifest() capability.Manifest {
	s := n.tool.Schema()
	return capability.Manifest{
		ID:          capability.ID(n.tool.ToolName()),
		Kind:        capability.KindTool,
		Name:        s.Name,
		Description: s.Description,
		// Version and Permissions are intentionally zero — native tools
		// do not carry either today. They are filled in later sub-tickets
		// (C3 brings PermissionSet; version comes from the Manifest spec).
	}
}

func (n nativeCapability) Permissions() capability.PermissionSet {
	return capability.PermissionSet{}
}

// Execute marshals Input to JSON (preserving the native tool's argsJSON
// contract) and wraps the string result in capability.Output.
//
// Error handling mirrors NativeTool.Run: a non-nil error is returned as-is
// from Execute, with Output zeroed. Callers that want the legacy "string +
// error" shape stay on NativeTool during C1.
func (n nativeCapability) Execute(ctx context.Context, in capability.Input) (capability.Output, error) {
	var argsJSON string
	if in == nil {
		argsJSON = "{}"
	} else {
		b, err := json.Marshal(in)
		if err != nil {
			return capability.Output{}, fmt.Errorf("capability: marshal input: %w", err)
		}
		argsJSON = string(b)
	}
	out, err := n.tool.Run(ctx, argsJSON)
	if err != nil {
		return capability.Output{}, err
	}
	return capability.Output{Data: out}, nil
}
