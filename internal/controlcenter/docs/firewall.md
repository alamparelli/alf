---
category: Security
tags: firewall, proxy, security, network, rules, domains
order: 6
---

# Firewall

Control which external domains ALF can reach from inside the container.

## Why a firewall?

ALF runs Claude Code inside a Docker container. Claude can make HTTP requests — fetching APIs, installing packages, calling webhooks. The firewall sits between Claude and the internet, letting you see and control every outbound connection.

Think of it as a bouncer for network traffic. You decide who gets through.

## Quick start

1. Open the **Firewall** tab in the sidebar.
2. You'll see two sections: **Rules** (empty by default) and **Request Log**.
3. The mode starts as **Log Only** — nothing is blocked, but every request is recorded.
4. Watch the log fill up as ALF works. You'll see which domains Claude contacts.
5. Add rules to allow or deny specific domains.

That's it. You can run in log-only mode as long as you want before enforcing anything.

## Two modes

| Mode | What happens |
|------|-------------|
| **Log Only** | All requests pass through. Every request is logged with its domain, method, and path. Deny rules are matched but not enforced. |
| **Enforce** | Allow/deny rules are active. Denied requests get a `403 Forbidden` response. Allowed and unmatched requests pass through. |

Switch between modes using the segmented control at the top of the Firewall page. Changes apply immediately.

> Start with **Log Only** for a few days. Review the request log to understand what domains ALF actually needs before writing deny rules.

## How rules work

Rules are checked top-to-bottom. The first matching rule wins.

Each rule has two parts:

| Field | Description | Examples |
|-------|------------|---------|
| **Pattern** | Domain to match. Supports exact match, wildcard prefix, or catch-all. | `api.telegram.org`, `*.anthropic.com`, `*` |
| **Action** | What to do when the pattern matches. | `allow` or `deny` |

### Pattern matching

| Pattern | Matches | Doesn't match |
|---------|---------|---------------|
| `api.telegram.org` | `api.telegram.org` | `telegram.org`, `web.telegram.org` |
| `*.github.com` | `api.github.com`, `raw.github.com` | `github.com` (no subdomain) |
| `*` | Everything | — |

### Rule order matters

Rules are evaluated in order. Put specific rules first, broad rules last.

**Example — allow only essential services:**

| # | Pattern | Action |
|---|---------|--------|
| 1 | `*.anthropic.com` | allow |
| 2 | `api.telegram.org` | allow |
| 3 | `*.github.com` | allow |
| 4 | `*.npmjs.org` | allow |
| 5 | `*` | deny |

This blocks everything except Anthropic APIs, Telegram, GitHub, and npm.

**Example — block a specific domain:**

| # | Pattern | Action |
|---|---------|--------|
| 1 | `evil-site.com` | deny |

Unmatched requests pass through (no catch-all deny), so only `evil-site.com` is blocked.

## The request log

Every proxied request is recorded in a circular buffer of 500 entries. Oldest entries are dropped when the buffer is full.

Each entry shows:

| Column | Description |
|--------|------------|
| **Time** | When the request was made |
| **Method** | HTTP method (`GET`, `POST`, `CONNECT` for HTTPS) |
| **Host** | Target domain |
| **Path** | URL path (HTTP only — HTTPS shows `CONNECT`) |
| **Status** | Response status code, or `403` if blocked |
| **Blocked** | Whether the request was denied |

Use the **Clear** button (trash icon) to wipe the log.

> The log is stored in memory only. It resets when the container restarts.

## Managing rules from the UI

1. Click **Add Rule** to create a new rule.
2. Enter the domain pattern and select allow or deny.
3. Rules appear in the list — drag to reorder, click to edit, delete with the trash icon.
4. Click the mode toggle to switch between Log Only and Enforce.

Changes save automatically and take effect immediately. No restart needed.

## How it works under the hood

The firewall is an HTTP proxy running on port `4751` inside the container. Claude Code's outbound traffic is routed through it via the `HTTP_PROXY` and `HTTPS_PROXY` environment variables.

- **HTTP requests** — the proxy inspects the `Host` header, matches rules, and either forwards or blocks.
- **HTTPS requests** — the proxy handles `CONNECT` tunnels at the domain level. It does **not** perform TLS interception (no MITM). It can only see the target domain, not the request path or body.

Configuration is stored in `config.d/firewall.json`:

```json
{
  "mode": "log-only",
  "port": 4751,
  "rules": [
    { "pattern": "*.anthropic.com", "action": "allow" },
    { "pattern": "*", "action": "deny" }
  ]
}
```

## Common questions

**What happens if I have no rules?**
All traffic passes through. The log still records every request.

**Does the firewall see HTTPS content?**
No. It only sees the domain name for HTTPS connections. Request paths, headers, and bodies are encrypted end-to-end.

**Can I edit firewall.json directly?**
Yes. Go to **Home > Workspace**, navigate to `config.d/firewall.json`, and edit the JSON. Changes are picked up on next reload.

**What if I lock myself out?**
If you accidentally block Anthropic's API, ALF won't be able to respond. Switch back to **Log Only** mode from the Control Center UI — the UI doesn't go through the proxy.

## What's next

- [Setting Up Tiers](tier-setup.md) — control which models ALF uses
- [Getting Started](getting-started.md) — overview of all ALF features
