---
category: Usage
tags: sessions, new, start, conversation, context, best practices
order: 5
---

# Managing Conversations

How ALF remembers context within a conversation, and when to start fresh.

## What is a session?

Every time you chat with ALF, you're inside a **session**. A session is like a thread — ALF remembers what you said earlier and keeps the context flowing.

For example:
- You: "Review this code"
- ALF: reviews the code
- You: "Now fix the bug you found"
- ALF knows which bug you mean — same session.

Sessions also keep **skills active**. If you ask ALF to draft a tweet, the X/Twitter skill loads and stays active for the rest of the session. Follow-up messages like "schedule it" still have access to the skill.

## Context across backends

ALF preserves conversation context even when switching between different LLM backends (Claude CLI, OpenRouter, Ollama, etc.). A unified conversation store captures rich message history — including tool calls and their results — so the next LLM knows what was done regardless of which provider handled the previous message.

- **API tiers** (OpenRouter, Ollama): conversation history is sent as structured messages in the API request
- **CLI tiers with active session**: Claude's built-in `--resume` provides richer context (preferred when available)
- **CLI tiers after a backend switch**: conversation history is injected as a system prompt since the CLI session from the previous backend is stale

## When sessions expire

Sessions don't last forever. They expire after a period of inactivity (default: 30 minutes, configurable in `config.json`).

When a session expires:
- ALF loses the conversation context
- Active skills are cleared
- The next message starts a fresh session

## Starting fresh with /new

Type `/new` in Telegram or CC Chat to manually end your session and start fresh.

**When to use /new:**

| Situation | Why /new helps |
|-----------|---------------|
| Switching topics | ALF won't mix context from different tasks |
| ALF seems confused | A fresh session clears any accumulated misunderstandings |
| After a long task | Free up context space for the next thing |
| Before an important request | Gives ALF maximum context window for your task |

## Best practices

### Start fresh when you switch topics

If you just finished debugging code and now want to plan your week — type `/new` first. Without it, ALF carries over the code context, wasting space and potentially confusing the response.

### Use /new before complex tasks

Big tasks (architecture review, long writing, multi-step automation) need context space. Start fresh so ALF has the full window available.

### Don't worry about losing knowledge

`/new` only clears the conversation thread. ALF still has:
- Long-term memories (everything you've taught it)
- Context files (your notes, preferences)
- Skills (auto-loaded when triggered again)
- All your files and tools

Think of it as closing a browser tab — your bookmarks and history are still there.

### Let natural sessions flow

You don't need to `/new` after every message. Sessions are useful — they let you have back-and-forth conversations, iterate on drafts, and build on previous context.

A good rhythm:
- One session per task or topic
- `/new` when you're done or switching gears
- Let it expire naturally if you walk away

## The /start command

`/start` is different from `/new`. It:
1. Archives the current session (like `/new`)
2. Runs the onboarding flow again

Use `/start` if you want ALF to re-introduce itself, or after a major update.

## Common questions

**Can I go back to a previous session?**
No. Once a session is archived or expired, the conversation context is gone. Important information should be saved to memory or context files. The full message history (including tool calls) is still stored on disk in `logs/conversation.jsonl` for debugging.

**How do I know if my session is still active?**
If ALF responds with context from your recent messages, your session is active. If it seems to have forgotten, the session likely expired.

**Does switching tiers lose context?**
No. ALF tracks conversation history across all backends. If you're chatting on a Claude CLI tier and the router switches to an OpenRouter tier, the new provider receives the conversation history automatically.

**Does /new affect scheduled jobs?**
No. Scheduled jobs run independently of your chat sessions.

**What happens if a skill was active and the session expires?**
The skill clears from the session. Next time you mention a trigger keyword, it reloads automatically.

## What's next?

- [Getting Started](docs:getting-started) — ALF setup and overview
- [Creating Skills](docs:creating-skills) — teach ALF new abilities
