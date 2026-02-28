package gittrack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Tracker manages a git repository inside a data directory for version history.
type Tracker struct {
	dir      string
	mu       sync.Mutex
	interval time.Duration
	stopCh   chan struct{}
	stopped  bool
}

// New creates a Tracker for the given data directory.
func New(dataDir string) *Tracker {
	return &Tracker{
		dir:    dataDir,
		stopCh: make(chan struct{}),
	}
}

// Init initializes a git repo if one doesn't exist, writes .gitignore, and creates an initial commit.
func (t *Tracker) Init() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	gitDir := filepath.Join(t.dir, ".git")
	// Always mark directory as safe (required when daemon runs as root but dir is owned by another user).
	_ = t.git("config", "--global", "--add", "safe.directory", t.dir)
	if _, err := os.Stat(gitDir); err == nil {
		return nil // already initialized
	}

	if err := t.git("init"); err != nil {
		return fmt.Errorf("git init: %w", err)
	}
	if err := t.git("config", "user.name", "alf"); err != nil {
		return fmt.Errorf("git config user.name: %w", err)
	}
	if err := t.git("config", "user.email", "alf@local"); err != nil {
		return fmt.Errorf("git config user.email: %w", err)
	}

	if err := t.writeGitignore(); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}

	if err := t.git("add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := t.git("commit", "-m", "initial commit"); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}

	return nil
}

// Commit stages all changes and commits with the given message. No-op if clean.
func (t *Tracker) Commit(msg string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if err := t.git("add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	// Check if there are staged changes.
	if err := t.git("diff", "--cached", "--quiet"); err == nil {
		return nil // nothing to commit
	}

	if err := t.git("commit", "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	return nil
}

// SetInterval configures the periodic sweep interval.
func (t *Tracker) SetInterval(d time.Duration) {
	t.interval = d
}

// StartSweep launches a goroutine that periodically commits any changes.
func (t *Tracker) StartSweep() {
	if t.interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := t.Commit("auto: periodic sweep"); err != nil {
					fmt.Fprintf(os.Stderr, "gittrack sweep: %v\n", err)
				}
			case <-t.stopCh:
				return
			}
		}
	}()
}

// Stop halts the periodic sweep goroutine.
func (t *Tracker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.stopped {
		t.stopped = true
		close(t.stopCh)
	}
}

func (t *Tracker) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = t.dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

func (t *Tracker) writeGitignore() error {
	content := `*
!.gitignore
!.claude/
!.claude/**
!config.d/
!config.d/**
!tools/
!tools/**
!skills/
!skills/**
!memories/
!memories/**
!logs/
!logs/events/
!logs/events/*.jsonl
`
	return os.WriteFile(filepath.Join(t.dir, ".gitignore"), []byte(content), 0o644)
}
