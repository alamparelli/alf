//go:build linux

package tooling

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// ---------------------------------------------------------------------------
// SandboxedCmd — script structure tests
// ---------------------------------------------------------------------------

func buildSandboxedCmd(t *testing.T, command string, cfg SandboxConfig) (*exec.Cmd, string) {
	t.Helper()
	cmd := &exec.Cmd{}
	SandboxedCmd(cmd, command, cfg)
	if len(cmd.Args) < 3 {
		t.Fatal("expected cmd.Args to have at least 3 elements")
	}
	return cmd, cmd.Args[2] // the setup script is the third arg
}

func TestSandboxedCmd_CmdPath(t *testing.T) {
	cmd, _ := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppSlug:    "test",
		AppDataDir: "/data/apps/test",
	})
	if cmd.Path != "/bin/bash" {
		t.Errorf("cmd.Path = %q, want /bin/bash", cmd.Path)
	}
	if cmd.Args[0] != "bash" {
		t.Errorf("cmd.Args[0] = %q, want bash", cmd.Args[0])
	}
	if cmd.Args[1] != "-c" {
		t.Errorf("cmd.Args[1] = %q, want -c", cmd.Args[1])
	}
}

func TestSandboxedCmd_ScriptContainsSetE(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	if !strings.Contains(script, "set -e") {
		t.Error("script should contain 'set -e' for fail-fast")
	}
}

func TestSandboxedCmd_ScriptContainsTmpfsMount(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	if !strings.Contains(script, "mount -t tmpfs") {
		t.Error("script should mount tmpfs for new root")
	}
}

func TestSandboxedCmd_ScriptBindMountsSystemDirs(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	for _, dir := range []string{"bin", "usr", "lib", "sbin"} {
		if !strings.Contains(script, `"/$d"`) || !strings.Contains(script, dir) {
			// The loop iterates over bin usr lib sbin, so all should appear.
		}
	}
	// More direct: the for loop line should be present
	if !strings.Contains(script, "for d in bin usr lib sbin") {
		t.Error("script should bind-mount bin, usr, lib, sbin")
	}
}

func TestSandboxedCmd_ScriptCreatesDevDevices(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	for _, dev := range []string{"null", "zero", "urandom", "random"} {
		if !strings.Contains(script, dev) {
			t.Errorf("script should create /dev/%s", dev)
		}
	}
}

func TestSandboxedCmd_ScriptContainsChroot(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	if !strings.Contains(script, "chroot") {
		t.Error("script should use chroot")
	}
}

func TestSandboxedCmd_ScriptContainsSetprivDrop(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	if !strings.Contains(script, "setpriv --reuid=1000 --regid=1000 --init-groups --inh-caps=-all") {
		t.Error("script should drop to uid 1000 with --inh-caps=-all")
	}
}

func TestSandboxedCmd_ScriptContainsUlimits(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	ulimits := []string{"ulimit -u", "ulimit -f", "ulimit -t"}
	for _, u := range ulimits {
		if !strings.Contains(script, u) {
			t.Errorf("script should contain %q", u)
		}
	}
}

func TestSandboxedCmd_PlatformToolsBindMounted(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	// /opt/alf/bin must be mounted (tools.d symlinks resolve into it)
	if !strings.Contains(script, "/opt/alf/bin") {
		t.Error("script should bind-mount /opt/alf/bin")
	}
	if !strings.Contains(script, `mount -o remount,ro,bind "$NEWROOT/opt/alf/bin"`) {
		t.Error("/opt/alf/bin should be mounted read-only")
	}
	// /opt/alf/tools.d (the PATH-visible directory)
	if !strings.Contains(script, "/opt/alf/tools.d") {
		t.Error("script should bind-mount /opt/alf/tools.d")
	}
	if !strings.Contains(script, `mount -o remount,ro,bind "$NEWROOT/opt/alf/tools.d"`) {
		t.Error("tools.d should be mounted read-only")
	}
}

func TestSandboxedCmd_AppDataDirBindMounted(t *testing.T) {
	appDir := "/data/apps/myapp"
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppSlug:    "myapp",
		AppDataDir: appDir,
	})
	// The app data dir should appear as a bind mount target
	quoted := shellQuote(appDir)
	if !strings.Contains(script, "APP_DATA="+quoted) {
		t.Errorf("script should set APP_DATA=%s, script:\n%s", quoted, script)
	}
	if !strings.Contains(script, `mount --bind "$APP_DATA" "$NEWROOT$APP_DATA"`) {
		t.Error("script should bind-mount the app data dir")
	}
}

func TestSandboxedCmd_UserCommandQuoted(t *testing.T) {
	userCmd := `echo "hello world" && ls`
	_, script := buildSandboxedCmd(t, userCmd, SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	quoted := shellQuote(userCmd)
	if !strings.Contains(script, "/bin/bash -c "+quoted) {
		t.Errorf("script should end with /bin/bash -c %s", quoted)
	}
}

// ---------------------------------------------------------------------------
// SandboxedCmd — DNS snippet inclusion
// ---------------------------------------------------------------------------

func TestSandboxedCmd_NetworkTrue_IncludesDNS(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
		Network:    true,
	})
	if !strings.Contains(script, "resolv.conf") {
		t.Error("network=true: script should copy resolv.conf")
	}
}

func TestSandboxedCmd_NetworkFalse_NoDNS(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
		Network:    false,
	})
	if strings.Contains(script, "resolv.conf") {
		t.Error("network=false: script should NOT reference resolv.conf")
	}
}

// ---------------------------------------------------------------------------
// SandboxedCmd — Cloneflags
// ---------------------------------------------------------------------------

func TestSandboxedCmd_NetworkFalse_HasCLONE_NEWNET(t *testing.T) {
	cmd, _ := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
		Network:    false,
	})
	flags := cmd.SysProcAttr.Cloneflags
	if flags&syscall.CLONE_NEWNS == 0 {
		t.Error("CLONE_NEWNS should always be set")
	}
	if flags&syscall.CLONE_NEWPID == 0 {
		t.Error("CLONE_NEWPID should always be set")
	}
	if flags&syscall.CLONE_NEWNET == 0 {
		t.Error("network=false: CLONE_NEWNET should be set")
	}
}

func TestSandboxedCmd_NetworkTrue_NoCLONE_NEWNET(t *testing.T) {
	cmd, _ := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
		Network:    true,
	})
	flags := cmd.SysProcAttr.Cloneflags
	if flags&syscall.CLONE_NEWNS == 0 {
		t.Error("CLONE_NEWNS should always be set")
	}
	if flags&syscall.CLONE_NEWPID == 0 {
		t.Error("CLONE_NEWPID should always be set")
	}
	if flags&syscall.CLONE_NEWNET != 0 {
		t.Error("network=true: CLONE_NEWNET should NOT be set")
	}
}

// ---------------------------------------------------------------------------
// SandboxedCmd — Credential
// ---------------------------------------------------------------------------

func TestSandboxedCmd_CredentialRoot(t *testing.T) {
	cmd, _ := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	cred := cmd.SysProcAttr.Credential
	if cred == nil {
		t.Fatal("SysProcAttr.Credential should be set")
	}
	if cred.Uid != 0 {
		t.Errorf("Credential.Uid = %d, want 0", cred.Uid)
	}
	if cred.Gid != 0 {
		t.Errorf("Credential.Gid = %d, want 0", cred.Gid)
	}
}

// ---------------------------------------------------------------------------
// Security tests
// ---------------------------------------------------------------------------

func TestSandboxedCmd_NoSensitivePaths(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppSlug:    "test",
		AppDataDir: "/data/apps/test",
	})

	sensitivePaths := []string{
		"/opt/alf/vault-data",
		"/home/alf/.claude",
		"/opt/alf/config.d",
		"/opt/alf/skills.d",
		"/run/secrets",
		".env",
		"credentials",
	}

	for _, p := range sensitivePaths {
		// Check that the path is not bind-mounted. It's fine if it appears
		// in a comment or as part of the app data dir, but not as a mount source.
		if strings.Contains(script, "mount --bind \""+p) ||
			strings.Contains(script, "mount --rbind \""+p) ||
			strings.Contains(script, "mount --bind "+p) {
			t.Errorf("script should not bind-mount sensitive path: %s", p)
		}
	}
}

func TestSandboxedCmd_SetprivDropsAllCaps(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	if !strings.Contains(script, "--inh-caps=-all") {
		t.Error("setpriv should drop ALL capabilities with --inh-caps=-all")
	}
}

// ---------------------------------------------------------------------------
// SandboxServerCmd — helper
// ---------------------------------------------------------------------------

func buildSandboxServerCmd(t *testing.T, cfg ServerSandboxConfig) (*exec.Cmd, string) {
	t.Helper()
	cmd := exec.Command("/usr/bin/myserver", "--port", "8080")
	cmd.Env = ServerSafeEnv(cfg.AppDir)
	SandboxServerCmd(cmd, cfg)
	if len(cmd.Args) < 3 {
		t.Fatal("expected cmd.Args to have at least 3 elements")
	}
	return cmd, cmd.Args[2] // the setup script
}

// ---------------------------------------------------------------------------
// SandboxServerCmd — script structure
// ---------------------------------------------------------------------------

func TestSandboxServerCmd_ScriptStructure(t *testing.T) {
	_, script := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	required := []struct {
		desc    string
		pattern string
	}{
		{"tmpfs mount", "mount -t tmpfs"},
		{"bind-mount /bin /usr /lib", "for d in bin usr lib sbin"},
		{"chroot", "chroot"},
		{"setpriv", "setpriv"},
		{"resolv.conf always included", "resolv.conf"},
	}
	for _, r := range required {
		if !strings.Contains(script, r.pattern) {
			t.Errorf("script missing %s (expected %q)", r.desc, r.pattern)
		}
	}
}

func TestSandboxServerCmd_PlatformToolsBindMounted(t *testing.T) {
	_, script := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})
	if !strings.Contains(script, "/opt/alf/bin") {
		t.Error("server script should bind-mount /opt/alf/bin")
	}
	if !strings.Contains(script, "/opt/alf/tools.d") {
		t.Error("server script should bind-mount /opt/alf/tools.d")
	}
}

func TestSandboxServerCmd_MountsAppDir(t *testing.T) {
	appDir := "/data/apps/weather"
	_, script := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  appDir,
	})

	// The full app dir should be bind-mounted (not just data/).
	quoted := shellQuote(appDir)
	if !strings.Contains(script, "APP_DIR="+quoted) {
		t.Errorf("script should set APP_DIR=%s", quoted)
	}
	if !strings.Contains(script, `mount --bind "$APP_DIR" "$NEWROOT$APP_DIR"`) {
		t.Error("script should bind-mount the full app dir")
	}
}

func TestSandboxServerCmd_NoNetworkNamespace(t *testing.T) {
	cmd, _ := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	flags := cmd.SysProcAttr.Cloneflags
	if flags&syscall.CLONE_NEWNET != 0 {
		t.Error("CLONE_NEWNET should NOT be set — server needs network access")
	}
}

func TestSandboxServerCmd_HasPidNamespace(t *testing.T) {
	cmd, _ := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	flags := cmd.SysProcAttr.Cloneflags
	if flags&syscall.CLONE_NEWPID == 0 {
		t.Error("CLONE_NEWPID should be set for PID isolation")
	}
}

func TestSandboxServerCmd_HasMountNamespace(t *testing.T) {
	cmd, _ := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	flags := cmd.SysProcAttr.Cloneflags
	if flags&syscall.CLONE_NEWNS == 0 {
		t.Error("CLONE_NEWNS should be set for mount isolation")
	}
}

func TestSandboxServerCmd_RootCredential(t *testing.T) {
	cmd, _ := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	cred := cmd.SysProcAttr.Credential
	if cred == nil {
		t.Fatal("SysProcAttr.Credential should be set")
	}
	if cred.Uid != 0 {
		t.Errorf("Credential.Uid = %d, want 0 (root for mount setup)", cred.Uid)
	}
	if cred.Gid != 0 {
		t.Errorf("Credential.Gid = %d, want 0", cred.Gid)
	}
}

func TestSandboxServerCmd_DropsAllCaps(t *testing.T) {
	_, script := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	if !strings.Contains(script, "--inh-caps=-all") {
		t.Error("setpriv should drop ALL capabilities with --inh-caps=-all")
	}
}

func TestSandboxServerCmd_NoSensitivePaths(t *testing.T) {
	_, script := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "test",
		AppDir:  "/data/apps/test",
	})

	sensitivePaths := []string{
		"/opt/alf/vault-data",
		"/home/alf/.claude",
		"/run/secrets",
	}

	for _, p := range sensitivePaths {
		if strings.Contains(script, "mount --bind \""+p) ||
			strings.Contains(script, "mount --rbind \""+p) ||
			strings.Contains(script, "mount --bind "+p) ||
			strings.Contains(script, "mount --rbind "+p) {
			t.Errorf("script should not bind-mount sensitive path: %s", p)
		}
	}
}

func TestSandboxServerCmd_PassesCommandViaEnv(t *testing.T) {
	cmd, _ := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	foundCmd := false
	foundArgs := false
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "__SANDBOX_SERVER_CMD=") {
			foundCmd = true
			val := strings.TrimPrefix(e, "__SANDBOX_SERVER_CMD=")
			if val != "/usr/bin/myserver" {
				t.Errorf("__SANDBOX_SERVER_CMD = %q, want /usr/bin/myserver", val)
			}
		}
		if strings.HasPrefix(e, "__SANDBOX_SERVER_ARGS=") {
			foundArgs = true
		}
	}
	if !foundCmd {
		t.Error("cmd.Env should contain __SANDBOX_SERVER_CMD")
	}
	if !foundArgs {
		t.Error("cmd.Env should contain __SANDBOX_SERVER_ARGS")
	}
}

func TestSandboxServerCmd_NoUlimits(t *testing.T) {
	_, script := buildSandboxServerCmd(t, ServerSandboxConfig{
		AppSlug: "weather",
		AppDir:  "/data/apps/weather",
	})

	if strings.Contains(script, "ulimit") {
		t.Error("server sandbox should not contain ulimit — servers need sustained resources")
	}
}

// ---------------------------------------------------------------------------
// SandboxedCmd — injection tests (existing)
// ---------------------------------------------------------------------------

func TestSandboxedCmd_ShellQuoteBlocksInjection(t *testing.T) {
	injections := []struct {
		name    string
		command string
		// None of these should result in unquoted shell metacharacters
	}{
		{"backtick injection", "`rm -rf /`"},
		{"command substitution", "$(cat /etc/shadow)"},
		{"semicolon breakout", "echo hi; rm -rf /"},
		{"pipe injection", "echo | cat /etc/passwd"},
		{"ampersand background", "sleep 999 &"},
		{"redirect", "echo > /etc/passwd"},
		{"newline injection", "echo\nrm -rf /"},
		{"null byte injection", "echo\x00rm -rf /"},
	}

	for _, tt := range injections {
		t.Run(tt.name, func(t *testing.T) {
			_, script := buildSandboxedCmd(t, tt.command, SandboxConfig{
				AppDataDir: "/data/apps/test",
			})
			// The user command must appear only inside single quotes.
			// After /bin/bash -c, the next thing should be a single-quoted string.
			idx := strings.LastIndex(script, "/bin/bash -c ")
			if idx == -1 {
				t.Fatal("script should contain /bin/bash -c")
			}
			remainder := script[idx+len("/bin/bash -c "):]
			remainder = strings.TrimRight(remainder, "\n")
			if !strings.HasPrefix(remainder, "'") {
				t.Errorf("user command should be single-quoted, got: %q", remainder)
			}
			if !strings.HasSuffix(remainder, "'") {
				t.Errorf("user command should end with single quote, got: %q", remainder)
			}
		})
	}
}

func TestSandboxedCmd_ScriptHasFailFast(t *testing.T) {
	_, script := buildSandboxedCmd(t, "echo hi", SandboxConfig{
		AppDataDir: "/data/apps/test",
	})
	// set -e must be near the top of the script
	lines := strings.Split(script, "\n")
	foundSetE := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "set -e" {
			foundSetE = true
			break
		}
		if i > 5 {
			// set -e should be in the first few non-empty, non-comment lines
			break
		}
	}
	if !foundSetE {
		t.Error("set -e should appear near the top of the script")
	}
}
