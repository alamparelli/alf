---
category: Basics
tags: chat, conversations, media, reactions, links
order: 2
---

# Chat

Send messages to ALF and get responses powered by Claude. ALF automatically picks the right model based on your configured tiers, or you can force a specific one.

## Sending messages

Type in the input bar and press **Enter** to send. Shift+Enter inserts a newline.

While ALF is generating a response you'll see a typing indicator. You can keep typing -- messages are queued automatically and sent in order once the current response finishes. Queued messages show a "queued #N" badge and can be cancelled individually.

Press the **Stop** button (red square) to interrupt a response mid-stream.

## Conversations

Each chat session is a conversation. Click the **New** button (top-left) to start a fresh conversation. You can also type `/new` or `/clear` in the input bar.

The current conversation persists across page reloads. When you return to the Control Center, you pick up where you left off.

## Tier selector

A dropdown appears next to the send button when you have multiple tiers configured. Options:

| Selection | Behavior |
|-----------|----------|
| **Auto** | ALF's router picks the best tier for the message (default) |
| **Specific tier** | Forces that tier for the next message |

The dropdown lists all tiers from your configuration by name.

## Slash commands

Type `/` in the input bar to see available commands. Arrow keys to navigate, Tab or Enter to select.

| Command | Action |
|---------|--------|
| `/new` | Start a new conversation |
| `/clear` | Alias for `/new` |
| `/skills` | Navigate to the Skills view |
| `/<tier-name>` | Force a specific tier for the message |

## Media attachments

Attach files by clicking the **paperclip** button, dragging files onto the input area, or pasting an image from clipboard.

Supported media types:

- **Images** -- shown as thumbnails in the message, click to view full size in a lightbox
- **Documents** (PDFs, etc.) -- shown as file name chips
- **Video / Audio** -- uploaded and sent as attachments

Multiple files can be attached to a single message. Remove an attachment before sending by clicking the X on its chip.

## Message details

Each assistant message shows metadata in the footer:

| Field | Description |
|-------|-------------|
| **Time** | When the message was sent |
| **Tier / Model** | Which tier or model generated the response |
| **Cost** | USD cost of the API call (e.g. $0.0032) |
| **Duration** | Response time in seconds |
| **Skills** | Which skills were active for the response |

## Content blocks

Assistant responses can contain structured content blocks:

- **Thinking** -- the model's chain-of-thought reasoning. Collapsed by default; click to expand.
- **Tool use** -- tools the model called (file edits, bash commands, etc.). Collapsed by default; shows tool name in the header and input parameters when expanded.
- **Tool result** -- output returned by a tool call, shown as indented text.
- **Text** -- the actual response, rendered as markdown with syntax highlighting, lists, tables, and inline media.

Bare image/GIF/video URLs on their own line are auto-rendered inline. Video URLs render as playable `<video>` elements.

## Block filter

A button group in the chat header lets you control which content blocks are visible:

| Mode | Visible blocks | Use case |
|------|---------------|----------|
| **All** | Text + Thinking + Tools | Full debug view -- see everything the model does |
| **Clean** | Text only | Cleanest view -- like a normal chat, no internal details |
| **Thinking** | Text + Thinking | Follow the model's reasoning without tool call noise |
| **Tools** | Text + Tools | Follow the model's actions without reasoning traces |

The selected filter is persisted in your browser across sessions. Each block type (thinking, tool use, tool result) is completely hidden when filtered out -- not just collapsed.

## Internal links

ALF can include clickable links in responses that navigate directly within the Control Center:

| Link format | Navigates to |
|-------------|-------------|
| `[label](alf://files/path/to/file)` | Opens the file in the Workspace viewer |
| `[label](alf://dirs/path/to/dir)` | Opens the directory in the Workspace browser |
| `[label](alf://apps/app-name)` | Opens a marketplace app |
| `[label](alf://view/tasks)` | Navigates to a Control Center view (tasks, skills, etc.) |

These links look like normal underlined text. Clicking them switches to the appropriate view without leaving the app.

## Reactions

Click the **smiley face** icon on any assistant message to react with an emoji. A quick picker offers 10 common reactions. ALF may respond with a mirror reaction.

## Copy and send to agents

Each assistant message has action buttons in the footer:

- **Copy** (clipboard icon) -- copies the message text to your clipboard
- **Send to agents** (people icon) -- opens a modal to launch the message as an agent task. Available when the message is longer than 10 characters. You can choose a team, edit the prompt, and optionally enable plan review before execution.

## Message queue

If you send a message while ALF is still responding, it enters a queue. Queued messages appear below the streaming response with a dashed border and a cancel button. They are processed in order.

## Notifications

When a response arrives while you're on another browser tab, ALF sends a desktop notification (if you've granted permission) and plays a sound. The chat badge on the sidebar also increments.

## What's next

- [Setting Up Tiers](tier-setup.md) -- configure which models are available
- [Creating Skills](creating-skills.md) -- add skills that ALF can use in chat
- [Workspace](workspace.md) -- browse files ALF creates during conversations
