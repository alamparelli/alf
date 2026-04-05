package tooling

import "context"

// Service interfaces for system native tools.
// These interfaces are implemented by adapters in cmd/alf-daemon that wrap
// the real subsystem objects, avoiding import cycles (tooling cannot import
// controlcenter, agents, etc.).

// --- Task/Orchestrator ---

// TaskService provides orchestrator task management.
type TaskService interface {
	Launch(ctx context.Context, opts TaskLaunchOpts) (taskID string, err error)
	List() []TaskInfo
	Cancel(id string) bool
	Delete(id string) bool
	Approve(id string, approved bool, feedback string) bool
}

// TaskLaunchOpts configures a new task launch.
type TaskLaunchOpts struct {
	Prompt         string
	Tier           string
	Team           string
	Skills         []string
	NeedValidation bool
}

// TaskInfo is a lightweight snapshot of a task.
type TaskInfo struct {
	ID         string `json:"id"`
	Prompt     string `json:"prompt"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	Team       string `json:"team,omitempty"`
	Iterations int    `json:"iterations"`
	Response   string `json:"response,omitempty"`
}

// --- Teams ---

// TeamService provides agent team CRUD.
type TeamService interface {
	All() []TeamInfo
	Get(name string) (*TeamInfo, bool)
	Save(req TeamSaveRequest) error
	Delete(nameOrID string) error
}

// TeamInfo describes an agent team.
type TeamInfo struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Agents      []AgentInfo `json:"agents"`
}

// AgentInfo describes a single agent within a team.
type AgentInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tier        string   `json:"tier"`
	Skills      []string `json:"skills,omitempty"`
}

// TeamSaveRequest is the input for creating or updating a team.
type TeamSaveRequest struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Agents      []AgentInfo `json:"agents"`
}

// --- Skills ---

// SkillService provides skill catalog access.
type SkillService interface {
	All() []SkillInfo
	Get(name string) (*SkillDetail, bool)
}

// SkillInfo is a summary of a skill.
type SkillInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Triggers    []string `json:"triggers,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	Source      string   `json:"source"` // "system" or "user"
}

// SkillDetail includes the skill content.
type SkillDetail struct {
	SkillInfo
	Content string `json:"content"`
}

// --- Apps / Marketplace ---

// AppService provides app listing and marketplace operations.
type AppService interface {
	List() []AppInfo
	Catalog() ([]RemoteAppInfo, error)
	Install(slug string) error
	Update(slug string) error
	Enable(slug string) error
	Disable(slug string) error
	Uninstall(slug string) error
	Restart(slug string) error
	ServiceStatus() []ServiceStatusInfo
}

// ServiceStatusInfo describes the runtime state of an app service.
type ServiceStatusInfo struct {
	AppSlug   string `json:"app"`
	Name      string `json:"name"`
	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Restarts  int    `json:"restarts"`
	StartedAt string `json:"started_at,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// AppInfo describes an installed app.
type AppInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"` // "enabled", "disabled", "installed"
}

// RemoteAppInfo describes a marketplace app.
type RemoteAppInfo struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
}

// --- Config ---

// ConfigService provides read access to system configuration.
type ConfigService interface {
	Get() (map[string]any, error)
}

// --- Tiers ---

// TierService provides tier listing.
type TierService interface {
	List() []TierInfo
}

// TierInfo describes an LLM tier.
type TierInfo struct {
	Name        string   `json:"name"`
	Model       string   `json:"model"`
	Backend     string   `json:"backend,omitempty"`
	Enabled     bool     `json:"enabled"`
	Routable    bool     `json:"routable"`
	Tools       []string `json:"tools,omitempty"`
	Effort      string   `json:"effort,omitempty"`
	Description string   `json:"description,omitempty"`
}

// --- Logs ---

// LogService provides log file access.
type LogService interface {
	Available() []string
	Tail(name string, lines int) ([]string, error)
}

// --- LLM Invoke ---

// LLMService provides direct LLM tier invocation for one-shot calls.
type LLMService interface {
	Invoke(ctx context.Context, opts LLMInvokeOpts) (string, error)
}

// LLMInvokeOpts configures a one-shot LLM call or a fire-and-forget chain.
type LLMInvokeOpts struct {
	Tier          string          // tier name (dynamic, from tier config)
	Prompt        string          // user prompt
	System        string          // optional system prompt
	FireAndForget bool            // if true, run async and call OnComplete with result
	OnComplete    *LLMOnComplete  // required when FireAndForget=true
	MaxDepth      int             // max chain depth (set on first call, decremented)
	ChainID       string          // UUID propagated through the chain
}

// LLMOnComplete defines the next step in a fire-and-forget chain.
// The prompt may contain {result} which is replaced with the previous step's output
// wrapped in <chain_result status="N">...</chain_result>.
type LLMOnComplete struct {
	Tier          string         `json:"tier"`
	Prompt        string         `json:"prompt"`
	System        string         `json:"system,omitempty"`
	FireAndForget bool           `json:"fire_and_forget,omitempty"`
	OnComplete    *LLMOnComplete `json:"on_complete,omitempty"`
}

// LLMChainResult is the structured result passed between chain steps.
type LLMChainResult struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

// --- Search ---

// SearchService provides cross-resource search.
type SearchService interface {
	Search(query string, types []string) ([]SearchResult, error)
}

// SearchResult is a single search hit.
type SearchResult struct {
	Type string `json:"type"` // "app", "file", "doc"
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	Desc string `json:"description,omitempty"`
}
