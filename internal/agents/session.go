package agents

import "sync"

// SessionManager tracks Claude session IDs within one orchestration run.
// Maps agent key (e.g. "orchestrator", "team/agent") to its session ID
// so the same agent can be resumed across iterations.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]string
}

func newSessionManager() *SessionManager {
	return &SessionManager{sessions: make(map[string]string)}
}

func (sm *SessionManager) Get(key string) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessions[key]
}

func (sm *SessionManager) Set(key, sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[key] = sessionID
}

func (sm *SessionManager) Clear(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, key)
}
