package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alamparelli/alf/internal/capability"
)

// CommandCapabilityID is the registry ID for the direct-tier bash runner.
const CommandCapabilityID capability.ID = "scheduler.command"

// commandCapabilityDefaultTimeout matches the legacy runCommand default.
const commandCapabilityDefaultTimeout = 2 * time.Minute

// commandCapabilityMaxOutput caps captured stdout+stderr, matching runCommand.
const commandCapabilityMaxOutput = 4000

// CommandCapability executes a bash command on behalf of direct-tier scheduled
// jobs. It is the Capability shape of the scheduler's legacy runCommand — the
// first consumer migration through Runtime.Invoke (#340 R5a). The Capability
// owns the environment shape (tools in PATH, ALF_SIGNAL_SOCK, secret stripping)
// so callers only supply {command, timeout}.
type CommandCapability struct {
	dataDir        string
	signalSockPath string
}

// NewCommandCapability returns the Capability configured for this daemon's
// data + signal socket paths. A zero-value CommandCapability is still valid:
// it just runs with the daemon env minus secrets.
func NewCommandCapability(dataDir, signalSockPath string) *CommandCapability {
	return &CommandCapability{dataDir: dataDir, signalSockPath: signalSockPath}
}

func (c *CommandCapability) Manifest() capability.Manifest {
	return capability.Manifest{
		ID:          CommandCapabilityID,
		Kind:        capability.KindTool,
		Name:        "scheduler.command",
		Description: "Run a bash command for a direct-tier scheduled job.",
	}
}

func (c *CommandCapability) Permissions() capability.PermissionSet {
	return capability.PermissionSet{}
}

// Execute runs bash -c "<command>" with a per-call timeout. Input keys:
//   - "command" (string, required): the bash expression to run.
//   - "timeout" (time.Duration or parseable string, optional): per-call limit.
//
// On timeout or non-zero exit Execute returns Output.Error populated AND a
// Go error — Runtime folds the error side into Output.Error on its own, but
// the double-surface matches what direct callers (Engine.runCommand path)
// expect today.
func (c *CommandCapability) Execute(ctx context.Context, in capability.Input) (capability.Output, error) {
	cmdStr, _ := in["command"].(string)
	if strings.TrimSpace(cmdStr) == "" {
		err := fmt.Errorf("command required")
		return capability.Output{Error: err.Error()}, err
	}

	timeout := commandCapabilityDefaultTimeout
	if v, ok := in["timeout"]; ok {
		switch tv := v.(type) {
		case time.Duration:
			if tv > 0 {
				timeout = tv
			}
		case string:
			if d, err := time.ParseDuration(tv); err == nil && d > 0 {
				timeout = d
			}
		}
	}

	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(rctx, "bash", "-c", cmdStr)
	cmd.Env = c.buildEnv()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	output := buf.String()
	if len(output) > commandCapabilityMaxOutput {
		output = output[:commandCapabilityMaxOutput] + "\n... (truncated)"
	}

	if runErr != nil {
		if rctx.Err() == context.DeadlineExceeded {
			err := fmt.Errorf("command timed out after %v", timeout)
			return capability.Output{Error: err.Error()}, err
		}
		if output != "" {
			err := fmt.Errorf("command failed: %w\n%s", runErr, output)
			return capability.Output{Error: err.Error()}, err
		}
		err := fmt.Errorf("command failed: %w", runErr)
		return capability.Output{Error: err.Error()}, err
	}

	return capability.Output{Data: strings.TrimSpace(output)}, nil
}

func (c *CommandCapability) buildEnv() []string {
	var env []string
	for _, v := range os.Environ() {
		if isSecretEnv(v) {
			continue
		}
		if strings.HasPrefix(v, "PATH=") && c.dataDir != "" {
			toolPaths := filepath.Join(c.dataDir, "tools.d") + ":" + filepath.Join(c.dataDir, "tools")
			v = "PATH=" + strings.TrimPrefix(v, "PATH=") + ":" + toolPaths
		}
		env = append(env, v)
	}
	if c.signalSockPath != "" {
		env = append(env, "ALF_SIGNAL_SOCK="+c.signalSockPath)
	}
	return env
}
