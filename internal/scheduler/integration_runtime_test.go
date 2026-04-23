package scheduler_test

import (
	"context"
	"testing"
	"time"

	"github.com/alamparelli/alf/internal/ai"
	"github.com/alamparelli/alf/internal/capability"
	"github.com/alamparelli/alf/internal/memory"
	"github.com/alamparelli/alf/internal/runtime"
	"github.com/alamparelli/alf/internal/sandbox"
	"github.com/alamparelli/alf/internal/scheduler"
)

// stubEngine is a placeholder ai.Engine for Runtime construction when only
// Invoke is exercised. Chat is never called in these tests, so the Run
// implementation just closes an empty channel.
type stubEngine struct{}

func (stubEngine) Run(_ context.Context, _ ai.Request) (<-chan ai.Event, error) {
	ch := make(chan ai.Event)
	close(ch)
	return ch, nil
}

// TestIntegration_SchedulerCommandThroughRuntime runs a scheduler direct-tier
// command via Runtime.Invoke on a real capability.Registry + sandbox.New().
// Proves the #340 R5a stack composes end-to-end: CommandCapability registers,
// Runtime resolves + applies sandbox policy, Capability.Execute runs bash,
// Output.Data flows back.
func TestIntegration_SchedulerCommandThroughRuntime(t *testing.T) {
	reg := capability.NewRegistry()
	if err := reg.Register(scheduler.NewCommandCapability("", "")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	store, err := memory.NewSQLiteStore("")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rt, err := runtime.New(runtime.Deps{
		Registry: reg,
		Memory:   store,
		AI:       stubEngine{},
		Sandbox:  sandbox.New(),
	}, runtime.Options{Tier: sandbox.Tier("direct")})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	out, err := rt.Invoke(context.Background(), scheduler.CommandCapabilityID, runtime.Args{
		"command": "echo runtime-invoke",
		"timeout": 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Error != "" {
		t.Fatalf("Output.Error: %q", out.Error)
	}
	s, _ := out.Data.(string)
	if s != "runtime-invoke" {
		t.Fatalf("Output.Data: got %q want %q", s, "runtime-invoke")
	}
}
