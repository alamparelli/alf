package comms

import (
	"fmt"
	"log"
	"strings"

	"github.com/alamparelli/alf/internal/conversation"
)

// CommandHandler processes a slash command. Returns a text response.
type CommandHandler func(e *ChatEngine, channelID ChannelID, args string) string

// CommandDef defines a registered command.
type CommandDef struct {
	Name    string
	Handler CommandHandler
	Admin   bool // requires admin channel
}

// CommandRegistry manages registered slash commands.
type CommandRegistry struct {
	commands map[string]CommandDef
}

// NewCommandRegistry creates a registry with built-in commands.
func NewCommandRegistry() *CommandRegistry {
	r := &CommandRegistry{commands: make(map[string]CommandDef)}
	r.Register(CommandDef{Name: "new", Handler: cmdNew})
	r.Register(CommandDef{Name: "clear", Handler: cmdNew}) // alias
	r.Register(CommandDef{Name: "skills", Handler: cmdSkills})
	r.Register(CommandDef{Name: "tool", Handler: cmdTool})
	return r
}

// Register adds a command to the registry.
func (r *CommandRegistry) Register(def CommandDef) {
	r.commands[def.Name] = def
}

// Dispatch executes a command by name. Returns (response, handled).
func (r *CommandRegistry) Dispatch(e *ChatEngine, channelID ChannelID, command, args string) (string, bool) {
	def, ok := r.commands[command]
	if !ok {
		return "", false
	}
	response := def.Handler(e, channelID, args)
	return response, true
}

// Get returns a command definition by name, if registered.
func (r *CommandRegistry) Get(name string) (CommandDef, bool) {
	def, ok := r.commands[name]
	return def, ok
}

// cmdNew handles /new and /clear: delegates to engine.NewSession.
func cmdNew(e *ChatEngine, channelID ChannelID, args string) string {
	old := e.NewSession(channelID, false)
	if old != "" {
		return "Previous session archived. New session started."
	}
	return "New session started."
}

// cmdStart handles /start: onboarding variant of /new.
func cmdStart(e *ChatEngine, channelID ChannelID, args string) string {
	e.NewSession(channelID, true)
	return "" // empty = fall through to process "hello" as onboarding
}

// cmdSkills handles /skills and /skills clear.
func cmdSkills(e *ChatEngine, channelID ChannelID, args string) string {
	sessionKey := channelID.SessionKey()

	if args == "clear" || args == "reset" {
		e.Sessions.ClearSkills(sessionKey)
		return "Active skills cleared from session."
	}

	active := e.Sessions.GetSkills(sessionKey)
	if len(active) == 0 {
		return "No skills active in this session.\n\nUse /skills clear to reset."
	}

	var lines []string
	for _, s := range active {
		lines = append(lines, "- "+s)
	}
	return fmt.Sprintf("Active skills:\n%s\n\nUse /skills clear to reset.", strings.Join(lines, "\n"))
}

// cmdTool handles /tool: manage quarantined tools.
// /tool — list quarantined tools
// /tool keep <name> — approve modified version
// /tool revert <name> — revert to original
func cmdTool(e *ChatEngine, channelID ChannelID, args string) string {
	if e.ToolExecutor == nil || e.ToolExecutor.Integrity == nil {
		return "Tool integrity guard is not enabled."
	}
	ig := e.ToolExecutor.Integrity

	parts := strings.Fields(args)
	if len(parts) == 0 {
		quarantined := ig.Quarantined()
		if len(quarantined) == 0 {
			return "No quarantined tools."
		}
		var lines []string
		for _, qt := range quarantined {
			lines = append(lines, fmt.Sprintf("- %s (old: %s, new: %s)", qt.Name, qt.OldHash[:12], qt.NewHash[:12]))
		}
		return fmt.Sprintf("Quarantined tools:\n%s\n\nUse /tool keep <name> or /tool revert <name>", strings.Join(lines, "\n"))
	}

	if len(parts) < 2 {
		return "Usage: /tool keep <name> | /tool revert <name>"
	}

	action, name := parts[0], parts[1]
	switch action {
	case "keep":
		if err := ig.ApproveModified(name); err != nil {
			return fmt.Sprintf("Failed: %v", err)
		}
		e.ToolRegistry.Rescan()
		return fmt.Sprintf("Tool %q approved. Modified version is now active.", name)
	case "revert":
		if err := ig.RevertTool(name); err != nil {
			return fmt.Sprintf("Failed: %v", err)
		}
		return fmt.Sprintf("Tool %q reverted to original.", name)
	default:
		return "Usage: /tool keep <name> | /tool revert <name>"
	}
}

// ProcessCommand checks if a message is a command and handles it.
// Returns (response, handled). If handled=true, the caller should send the response
// and skip further processing. If response="" and handled=true, fall through to Process().
func (e *ChatEngine) ProcessCommand(channelID ChannelID, text string, commands *CommandRegistry) (string, bool) {
	if !strings.HasPrefix(text, "/") {
		return "", false
	}

	parts := strings.SplitN(text, " ", 2)
	cmd := strings.TrimPrefix(parts[0], "/")
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	response, handled := commands.Dispatch(e, channelID, cmd, args)
	if handled {
		log.Printf("[comms] command /%s handled (channel=%s)", cmd, channelID.Prefix())
	}
	return response, handled
}

// HandleCommand checks if a message is a built-in command and handles it.
// Returns (response, handled). Convenience wrapper around ProcessCommand with default registry.
func (e *ChatEngine) HandleCommand(channelID ChannelID, text string) (string, bool) {
	return e.ProcessCommand(channelID, text, NewCommandRegistry())
}

// CheckForceCommand checks if a message is a /<tier_name> force command.
// Returns the tier name and remaining message, or ("", "") if not a force command.
func CheckForceCommand(text string, tiers []TierInfo) (tierName, remaining string) {
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	cmdName := strings.TrimPrefix(parts[0], "/")
	for _, t := range tiers {
		if t.ForceCommand && t.Name == cmdName {
			msg := ""
			if len(parts) > 1 {
				msg = strings.TrimSpace(parts[1])
			}
			return t.Name, msg
		}
	}
	return "", ""
}

// newConversation helper rotates conversation ID on the store.
func newConversation(store *conversation.Store, channel string) string {
	if store == nil {
		return ""
	}
	store.NewConversation(channel)
	return store.ConvID(channel)
}
