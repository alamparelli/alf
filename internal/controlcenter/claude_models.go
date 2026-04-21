package controlcenter

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alamparelli/alf/internal/ai"
)

//go:embed defaults/claude_models.txt
var defaultClaudeModelsTxt []byte

// ClaudeModelsStore holds the list of Claude model identifiers that the
// daemon accepts for CLI-backend tier validation. Backed by a user-editable
// text file; falls back to the embedded default when the file is missing
// or parses to an empty list.
type ClaudeModelsStore struct {
	pathVal atomic.Value // string
	mu      sync.RWMutex
	current atomic.Pointer[[]string]
}

// NewFileClaudeModelsStore creates a store backed by the given file path.
// The store is initialised with the embedded default list; call Reload()
// to load the file contents.
func NewFileClaudeModelsStore(path string) *ClaudeModelsStore {
	s := &ClaudeModelsStore{}
	s.pathVal.Store(path)
	initial := parseClaudeModels(defaultClaudeModelsTxt)
	s.current.Store(&initial)
	return s
}

// Path returns the current backing file path.
func (s *ClaudeModelsStore) Path() string {
	return s.pathVal.Load().(string)
}

// Current returns an immutable snapshot of the active model list.
func (s *ClaudeModelsStore) Current() []string {
	if p := s.current.Load(); p != nil {
		return *p
	}
	return nil
}

// Contains reports whether name is present in the current list.
func (s *ClaudeModelsStore) Contains(name string) bool {
	for _, m := range s.Current() {
		if m == name {
			return true
		}
	}
	return false
}

// Reload re-reads the backing file and swaps the in-memory list atomically.
// If the file does not exist or parses to an empty list, the embedded
// default is used so tier validation never collapses to "everything invalid".
func (s *ClaudeModelsStore) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.Path()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			list := parseClaudeModels(defaultClaudeModelsTxt)
			s.current.Store(&list)
			return nil
		}
		return fmt.Errorf("read claude_models: %w", err)
	}
	list := parseClaudeModels(data)
	if len(list) == 0 {
		list = parseClaudeModels(defaultClaudeModelsTxt)
	}
	s.current.Store(&list)
	return nil
}

// DefaultClaudeModelsTxt returns the raw embedded default file bytes.
// Used by bootstrap seeding to write the file on first run.
func DefaultClaudeModelsTxt() []byte {
	return defaultClaudeModelsTxt
}

// ClaudeModelsPath returns the default user-editable file location.
func ClaudeModelsPath(configDir string) string {
	return filepath.Join(configDir, "claude_models.txt")
}

// globalClaudeModelsStore holds the process-wide store used by tier
// validation. Set once at daemon startup via SetClaudeModelsStore.
// When nil (e.g. in unit tests that don't boot the daemon), validation
// falls back to the embedded default list.
var globalClaudeModelsStore atomic.Pointer[ClaudeModelsStore]

// SetClaudeModelsStore registers the process-wide Claude models store.
func SetClaudeModelsStore(s *ClaudeModelsStore) {
	globalClaudeModelsStore.Store(s)
}

// IsValidClaudeModel reports whether model is a recognised Claude model
// identifier for cli-backend tier configuration. It accepts:
//   - short aliases that resolve via internal/ai.ResolveModel (haiku,
//     sonnet, opus, sonnet-max, opus-max)
//   - any model ID starting with "claude-" (resolver pass-through)
//   - any entry in the user-editable claude_models.txt file
func IsValidClaudeModel(model string) bool {
	if model == "" {
		return false
	}
	// Short aliases and claude-* pass-through are handled by the resolver.
	if ai.ResolveModel(model) != "" {
		return true
	}
	// User-editable list (runtime store, when set).
	if s := globalClaudeModelsStore.Load(); s != nil {
		return s.Contains(model)
	}
	// Tests without a daemon fall back to the embedded default list.
	for _, m := range parseClaudeModels(defaultClaudeModelsTxt) {
		if m == model {
			return true
		}
	}
	return false
}

// parseClaudeModels parses one model name per line. Blanks and lines
// starting with # (after trimming) are skipped. Duplicates are removed
// while preserving first-occurrence order.
func parseClaudeModels(data []byte) []string {
	seen := make(map[string]bool)
	var out []string
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}
