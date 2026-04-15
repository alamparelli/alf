package controlcenter

import "strings"

// SanitizeTierCommand rewrites a tier name into a Telegram-safe slash command
// alias: lowercase ASCII letters/digits/underscores only, hyphens mapped to
// underscores, anything else dropped, truncated to 32 chars.
//
// Telegram rejects the entire setMyCommands batch with BOT_COMMAND_INVALID if
// any command name doesn't match ^[a-z0-9_]{1,32}$, so tier names containing
// hyphens (e.g. the default "codex-fast") must be normalized before
// publishing. The CC chat and Telegram command matchers accept both the raw
// tier name and this alias so users can type either form.
func SanitizeTierCommand(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune('_')
		}
	}
	s := b.String()
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
