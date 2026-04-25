// Package admin marks the boundary between operations the LLM may
// drive and operations that modify the trust surface of the system.
// See ARCHITECTURE-SECURITY.md §6 + #395.
//
// The principle: any code path that grows trust (adding a key, signing
// a bundle, ratifying a pending action, unlocking the user-scope
// vault) lives under internal/admin/ and is reachable only from
// TTY-direct CLI commands or the dedicated CC admin trust domain. No
// capability, no Runtime tool dispatch, no LLM-driven HTTP route may
// import this subtree — the archtest TestAdminPackageBoundary pins
// the rule.
//
// Stage 1 of #395 ships the package marker + the pending-action queue
// surface + the archtest. Stage 2 brings the user-scope vault
// partition, ceiling-aware auto-sign, the CC /admin/ratify page, and
// SecretValue redaction (which will share crypto plumbing with #411).
package admin
