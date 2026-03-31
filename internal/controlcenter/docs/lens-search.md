---
category: Features
tags: search, lens, keyboard, shortcuts, navigation
order: 3
---

# Lens Search

A quick-access search overlay for navigating tabs, apps, files, docs, and marketplace entries.

## Opening Lens

- **Keyboard shortcut**: `Cmd+G` (Mac) / `Ctrl+G` (Windows/Linux). The default key is `G` and can be changed in Settings.
- **FAB button**: Click the floating search button in the UI, which dispatches the `alf:open-lens` event.
- Press `Escape` to close.

## What it searches

Results are grouped into five categories:

| Category | Source | What happens on select |
|----------|--------|----------------------|
| **System** | Built-in tabs (Chat, Home, Terminal, Schedules, etc.) | Navigates to that tab |
| **Apps** | Installed apps and skills | Opens the app page |
| **Marketplace** | Available marketplace entries (excludes disabled) | Navigates to the Marketplace tab |
| **Files** | Files and directories in the workspace | Opens the file or directory in the Workspace view |
| **Docs** | Embedded documentation pages | Opens the doc page |

System tabs are filtered locally for instant results. Apps, marketplace, files, and docs are searched via the `/api/search` endpoint with a 300ms debounce.

## Keyboard navigation

| Key | Action |
|-----|--------|
| `Arrow Up` / `Arrow Down` | Move selection through results |
| `Enter` | Open the selected result |
| `Escape` | Close Lens |

## Folder filters

Click the **Filter** button in the bottom-right corner of the Lens footer to toggle folder visibility for file search results.

- Checkboxes let you include or exclude top-level workspace directories.
- Internal directories (`.git`, `.claude`, `.cache`, `.local`, `node_modules`, `go-path`, `docs`) are always hidden.
- Filter preferences are saved in `localStorage` and persist across sessions.
- A badge on the filter button shows how many folders are currently excluded.

## Customizing the shortcut

The default shortcut key is `G` (used with `Cmd` or `Ctrl`). To change it, go to **Settings** and set a different single-character key. The new key is saved in `localStorage` and takes effect immediately.

## What's next

- [Getting Started](getting-started.md) -- overview of the Control Center interface
