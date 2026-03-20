// Package supervisor manages persistent background services declared in app directories.
// Each app can optionally include a service.json to declare a long-running process.
// The supervisor starts enabled services, restarts them on crash with exponential backoff,
// and stops them cleanly on daemon shutdown.
package supervisor

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ServiceConfig is the on-disk format of service.json inside an app directory.
type ServiceConfig struct {
	Name         string            `json:"name"`
	Command      string            `json:"command"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Restart      string            `json:"restart"`       // "always", "on-failure", "no"
	RestartDelay string            `json:"restart_delay"`  // e.g. "3s"
	MaxRestarts  int               `json:"max_restarts"`   // 0 = unlimited
	Enabled      bool              `json:"enabled"`
}

// ServiceStatus holds runtime state for a managed service.
type ServiceStatus struct {
	Name       string
	AppSlug    string
	Running    bool
	PID        int
	Restarts   int
	StartedAt  time.Time
	LastCrash  time.Time
	LastError  string
}

// Supervisor manages the lifecycle of app services.
type Supervisor struct {
	appsDir string
	mu      sync.Mutex
	procs   map[string]*managedProc
	stop    chan struct{}
}

type managedProc struct {
	config   ServiceConfig
	appSlug  string
	workDir  string
	cmd      *exec.Cmd
	stopCh   chan struct{}
	restarts int
	started  time.Time
	lastErr  string
	lastCrash time.Time
}

// New creates a supervisor that scans appsDir for service.json files.
// Services run under the same uid as the daemon (no privilege change).
func New(appsDir string) *Supervisor {
	return &Supervisor{
		appsDir: appsDir,
		procs:   make(map[string]*managedProc),
		stop:    make(chan struct{}),
	}
}

// Start scans for services and launches all enabled ones.
func (s *Supervisor) Start() {
	services := s.scan()
	if len(services) == 0 {
		return
	}

	log.Printf("supervisor: found %d service(s)", len(services))
	for slug, cfg := range services {
		if !cfg.Enabled {
			log.Printf("supervisor: [%s] disabled, skipping", slug)
			continue
		}
		s.startService(slug, cfg)
	}
}

// Stop sends SIGTERM to all managed services and waits for them to exit.
func (s *Supervisor) Stop() {
	close(s.stop)

	s.mu.Lock()
	procs := make([]*managedProc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()

	for _, p := range procs {
		close(p.stopCh)
		if p.cmd != nil && p.cmd.Process != nil {
			p.cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	// Grace period then force kill.
	time.Sleep(5 * time.Second)
	for _, p := range procs {
		if p.cmd != nil && p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
	}
}

// Status returns the current state of all managed services.
func (s *Supervisor) Status() []ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []ServiceStatus
	for _, p := range s.procs {
		running := p.cmd != nil && p.cmd.Process != nil && p.cmd.ProcessState == nil
		st := ServiceStatus{
			Name:      p.config.Name,
			AppSlug:   p.appSlug,
			Running:   running,
			Restarts:  p.restarts,
			StartedAt: p.started,
			LastError: p.lastErr,
			LastCrash: p.lastCrash,
		}
		if running {
			st.PID = p.cmd.Process.Pid
		}
		out = append(out, st)
	}
	return out
}

// scan reads all apps/{slug}/service.json files.
func (s *Supervisor) scan() map[string]ServiceConfig {
	entries, err := os.ReadDir(s.appsDir)
	if err != nil {
		return nil
	}

	services := make(map[string]ServiceConfig)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(s.appsDir, e.Name(), "service.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue // no service.json — not a service app
		}
		var cfg ServiceConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Printf("supervisor: [%s] invalid service.json: %v", e.Name(), err)
			continue
		}
		if cfg.Command == "" {
			log.Printf("supervisor: [%s] service.json missing command", e.Name())
			continue
		}
		if cfg.Name == "" {
			cfg.Name = e.Name()
		}
		if cfg.Restart == "" {
			cfg.Restart = "always"
		}
		// SEC-005: default safety cap to prevent infinite restart loops.
		if cfg.MaxRestarts == 0 {
			cfg.MaxRestarts = 100
		}
		services[e.Name()] = cfg
	}
	return services
}

func (s *Supervisor) startService(slug string, cfg ServiceConfig) {
	workDir := filepath.Join(s.appsDir, slug)

	p := &managedProc{
		config:  cfg,
		appSlug: slug,
		workDir: workDir,
		stopCh:  make(chan struct{}),
	}

	s.mu.Lock()
	s.procs[slug] = p
	s.mu.Unlock()

	go s.supervise(p)
}

func (s *Supervisor) supervise(p *managedProc) {
	baseDelay := 3 * time.Second
	if p.config.RestartDelay != "" {
		if d, err := time.ParseDuration(p.config.RestartDelay); err == nil {
			baseDelay = d
		}
	}

	delay := baseDelay
	maxDelay := 60 * time.Second

	for {
		// Check if stopped.
		select {
		case <-s.stop:
			return
		case <-p.stopCh:
			return
		default:
		}

		// Build command.
		cmd, err := s.buildCmd(p)
		if err != nil {
			p.lastErr = err.Error()
			log.Printf("supervisor: [%s] refused: %v", p.appSlug, err)
			return
		}
		p.cmd = cmd
		p.started = time.Now()

		log.Printf("supervisor: [%s] starting: %s %s", p.appSlug, p.config.Command, strings.Join(p.config.Args, " "))

		if err := cmd.Start(); err != nil {
			p.lastErr = err.Error()
			log.Printf("supervisor: [%s] failed to start: %v", p.appSlug, err)
			return
		}

		log.Printf("supervisor: [%s] running (pid=%d)", p.appSlug, cmd.Process.Pid)

		// Wait for exit.
		err = cmd.Wait()

		// Check if we were asked to stop.
		select {
		case <-s.stop:
			return
		case <-p.stopCh:
			return
		default:
		}

		// Process crashed.
		p.restarts++
		p.lastCrash = time.Now()
		if err != nil {
			p.lastErr = err.Error()
		}

		// Reset backoff if stable for 5 minutes.
		if time.Since(p.started) > 5*time.Minute {
			delay = baseDelay
		}

		log.Printf("supervisor: [%s] exited (restarts=%d, err=%v), restarting in %s", p.appSlug, p.restarts, err, delay)

		// Check restart policy.
		if p.config.Restart == "no" {
			log.Printf("supervisor: [%s] restart=no, not restarting", p.appSlug)
			return
		}
		if p.config.Restart == "on-failure" && err == nil {
			log.Printf("supervisor: [%s] exited cleanly, not restarting (restart=on-failure)", p.appSlug)
			return
		}
		if p.config.MaxRestarts > 0 && p.restarts >= p.config.MaxRestarts {
			log.Printf("supervisor: [%s] max restarts (%d) reached, giving up", p.appSlug, p.config.MaxRestarts)
			return
		}

		// Backoff wait.
		select {
		case <-time.After(delay):
		case <-s.stop:
			return
		case <-p.stopCh:
			return
		}

		// Exponential backoff.
		delay = delay * 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// safePrefixes lists environment variable prefixes safe to inherit from the daemon.
// This mirrors the allowlists in provider/cli.go and tooling/native_bash.go.
var safePrefixes = []string{
	"PATH=", "HOME=", "LANG=", "LC_", "TZ=", "TMPDIR=",
	"USER=", "LOGNAME=",
	"VAULT_TOKEN=", "VAULT_ADDR=",
	"ANTHROPIC_",
}

// inheritSafeEnv builds a child environment by filtering the daemon's env through safePrefixes.
func inheritSafeEnv() []string {
	env := make([]string, 0, 16)
	for _, e := range os.Environ() {
		for _, prefix := range safePrefixes {
			if strings.HasPrefix(e, prefix) {
				env = append(env, e)
				break
			}
		}
	}
	// Ensure minimum defaults.
	hasLang := false
	for _, e := range env {
		if strings.HasPrefix(e, "LANG=") {
			hasLang = true
			break
		}
	}
	if !hasLang {
		env = append(env, "LANG=C.UTF-8")
	}
	return env
}

// blockedEnvKeys prevents service.json from overriding security-sensitive env vars.
var blockedEnvKeys = map[string]bool{
	"PATH": true, "HOME": true, "SHELL": true, "USER": true,
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true,
}

func (s *Supervisor) buildCmd(p *managedProc) (*exec.Cmd, error) {
	command := p.config.Command
	// Resolve relative commands against workdir.
	if !filepath.IsAbs(command) {
		command = filepath.Join(p.workDir, command)
	}

	// SEC-001: Validate command stays within the apps directory.
	command = filepath.Clean(command)
	appsAbs := filepath.Clean(s.appsDir) + string(os.PathSeparator)
	if !strings.HasPrefix(command, appsAbs) {
		return nil, fmt.Errorf("command %q escapes apps directory", p.config.Command)
	}

	cmd := exec.Command(command, p.config.Args...)

	cmd.Dir = p.workDir

	// Build environment: inherit safe vars from daemon + service-specific env.
	cmd.Env = inheritSafeEnv()

	// Provide app data directory so services know where to store persistent data.
	dataDir := filepath.Join(p.workDir, "data")
	os.MkdirAll(dataDir, 0o755)
	cmd.Env = append(cmd.Env, "ALF_APP_DATA_DIR="+dataDir)

	// SEC-002: Block dangerous env overrides.
	for k, v := range p.config.Env {
		if blockedEnvKeys[strings.ToUpper(k)] || strings.HasPrefix(strings.ToUpper(k), "LD_") {
			log.Printf("supervisor: [%s] blocked env override %q", p.appSlug, k)
			continue
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Capture logs to daemon output.
	cmd.Stdout = &prefixWriter{prefix: "[" + p.appSlug + "] "}
	cmd.Stderr = &prefixWriter{prefix: "[" + p.appSlug + "] "}

	return cmd, nil
}

// prefixWriter prepends a prefix to each line written.
type prefixWriter struct {
	prefix string
	buf    []byte
}

const maxPrefixBuf = 64 * 1024 // SEC-004: cap buffer to prevent DoS from newline-less output

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := -1
		for i, b := range w.buf {
			if b == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		log.Printf("%s%s", w.prefix, line)
		w.buf = w.buf[idx+1:]
	}
	// Flush if buffer grows too large without newlines.
	if len(w.buf) > maxPrefixBuf {
		log.Printf("%s%s (truncated)", w.prefix, string(w.buf[:1024]))
		w.buf = w.buf[:0]
	}
	return len(p), nil
}
