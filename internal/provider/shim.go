// Package provider is a thin re-export shim. The AI block's provider surface
// now lives at internal/ai/provider (moved during #340 Step 4 A2). Existing
// consumers keep importing internal/provider until Runtime (#340 R3–R5)
// rewires them.
package provider

import (
	"syscall"
	"time"

	aiprov "github.com/alamparelli/alf/internal/ai/provider"
	"github.com/alamparelli/alf/internal/tooling"
)

// --- Types ---------------------------------------------------------------

type APIProvider = aiprov.APIProvider
type APIProviderConfig = aiprov.APIProviderConfig
type ClassifierConfig = aiprov.ClassifierConfig
type CLIClassifier = aiprov.CLIClassifier
type CLIProvider = aiprov.CLIProvider
type CodexProvider = aiprov.CodexProvider
type ClassifyResult = aiprov.ClassifyResult
type Classifier = aiprov.Classifier
type ContextMessage = aiprov.ContextMessage
type ContextToolCall = aiprov.ContextToolCall
type History = aiprov.History
type LLMLogger = aiprov.LLMLogger
type MediaEntry = aiprov.MediaEntry
type Message = aiprov.Message
type OnProgress = aiprov.OnProgress
type Params = aiprov.Params
type Provider = aiprov.Provider
type Registry = aiprov.Registry
type Result = aiprov.Result
type StreamEvent = aiprov.StreamEvent
type ToolCallRequest = aiprov.ToolCallRequest
type ToolCallResult = aiprov.ToolCallResult
type ToolExecutor = aiprov.ToolExecutor
type ToolingExecutorAdapter = aiprov.ToolingExecutorAdapter
type ToolLoop = aiprov.ToolLoop

// --- Constructors --------------------------------------------------------

func NewAPIProviderFromConfig(cfg APIProviderConfig, history *History) *APIProvider {
	return aiprov.NewAPIProviderFromConfig(cfg, history)
}

func NewAPIProvider(apiKey string, history *History) *APIProvider {
	return aiprov.NewAPIProvider(apiKey, history)
}

func NewCLIClassifier(cfg ClassifierConfig) *CLIClassifier {
	return aiprov.NewCLIClassifier(cfg)
}

func NewCLIProvider(homeDir, dataDir string, timeout time.Duration, cred *syscall.Credential) *CLIProvider {
	return aiprov.NewCLIProvider(homeDir, dataDir, timeout, cred)
}

func NewCodexProvider(dataDir string, timeout time.Duration, apiKey string, cred *syscall.Credential) *CodexProvider {
	return aiprov.NewCodexProvider(dataDir, timeout, apiKey, cred)
}

func NewHistory(dataDir string, maxMsgs int, expiry time.Duration) *History {
	return aiprov.NewHistory(dataDir, maxMsgs, expiry)
}

func NewRegistry(cli *CLIProvider) *Registry {
	return aiprov.NewRegistry(cli)
}

func NewToolingExecutorAdapter(exec *tooling.Executor) *ToolingExecutorAdapter {
	return aiprov.NewToolingExecutorAdapter(exec)
}

func NewToolLoop(api *APIProvider, executor ToolExecutor, tools []map[string]any, maxTurns int) *ToolLoop {
	return aiprov.NewToolLoop(api, executor, tools, maxTurns)
}

// --- Package-level side-effects -----------------------------------------

func InitLLMLog(dataDir string) { aiprov.InitLLMLog(dataDir) }
func CloseLLMLog()              { aiprov.CloseLLMLog() }
