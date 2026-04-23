package agents

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
)

// Store provides read access to agent team configurations.
type Store interface {
	All() []*TeamConfig
	Get(teamName string) (*TeamConfig, bool)
	GetAgent(teamSlash string) (*TeamConfig, *AgentConfig, bool) // "team/agent"
	Reload() error
}

type fileAgentStore struct {
	dir     string
	current atomic.Pointer[map[string]*TeamConfig]
}

// NewFileAgentStore creates a Store backed by JSON files in dir.
func NewFileAgentStore(dir string) Store {
	os.MkdirAll(dir, 0o755)
	s := &fileAgentStore{dir: dir}
	empty := make(map[string]*TeamConfig)
	s.current.Store(&empty)
	_ = s.Reload()
	return s
}

func (s *fileAgentStore) All() []*TeamConfig {
	m := s.current.Load()
	out := make([]*TeamConfig, 0, len(*m))
	for _, tc := range *m {
		out = append(out, tc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *fileAgentStore) Get(teamName string) (*TeamConfig, bool) {
	m := s.current.Load()
	tc, ok := (*m)[teamName]
	return tc, ok
}

func (s *fileAgentStore) GetAgent(teamSlash string) (*TeamConfig, *AgentConfig, bool) {
	team, agent := splitTeamAgent(teamSlash)
	tc, ok := s.Get(team)
	if !ok {
		return nil, nil, false
	}
	for i := range tc.Agents {
		if tc.Agents[i].Name == agent {
			return tc, &tc.Agents[i], true
		}
	}
	return tc, nil, false
}

func (s *fileAgentStore) Reload() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	merged := make(map[string]*TeamConfig)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			log.Printf("agents: skip %s: %v", e.Name(), err)
			continue
		}
		var tc TeamConfig
		if err := json.Unmarshal(data, &tc); err != nil {
			log.Printf("agents: skip %s: %v", e.Name(), err)
			continue
		}
		if tc.Name == "" {
			tc.Name = e.Name()[:len(e.Name())-5] // strip .json
		}
		merged[tc.Name] = &tc
	}

	s.current.Store(&merged)
	if len(merged) > 0 {
		log.Printf("agents: loaded %d teams from %s", len(merged), s.dir)
	}
	return nil
}

// splitTeamAgent splits "team/agent" into its parts.
func splitTeamAgent(s string) (string, string) {
	for i := range s {
		if s[i] == '/' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}
