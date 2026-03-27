---
category: Basics
tags: workspace, files, editor, json, upload
order: 3
---

# Workspace

The Workspace is ALF's file browser. It shows everything in the `data/` directory -- the files ALF creates, your configuration, skills, logs, and anything you upload.

## Layout

The view has three parts:

- **Toolbar** (top) -- buttons for new file, new folder, upload, and refresh
- **Sidebar** (left) -- a collapsible folder tree for quick navigation
- **File table** (right) -- lists the contents of the current directory with name, size, and date columns

Click a folder to navigate into it. Click a file to open it in the viewer. Breadcrumbs at the top show your current path and let you jump back to any parent.

## Sidebar tree

The sidebar shows all top-level directories. Click the chevron to expand a directory and see its subdirectories. Click the directory name to load its contents in the file table.

The root is labeled **data** and always appears at the top.

## Creating files and directories

Use the toolbar buttons:

- **New file** (file+ icon) -- creates an empty file in the current directory
- **New folder** (folder+ icon) -- creates a new directory in the current directory

A dialog asks for the name. The entry is created immediately.

## Editing files

Click any text file to open the viewer modal. The modal shows:

- File name and size
- Read-only badge if the file cannot be edited
- Action buttons depending on file type

To edit, click **Edit**. The content loads into a textarea. Click **Save** to write changes back, or **Cancel** to discard.

Binary files and files over 1 MB cannot be displayed or edited. A download link is shown instead.

## File formats

The viewer handles several formats with specialized rendering:

| Format | Behavior |
|--------|----------|
| **Markdown** (`.md`) | Rendered preview by default. Click **Source** to see raw text. |
| **JSON** (`.json`) | Raw view by default. Click **Tree** to switch to an interactive tree viewer. |
| **JSONL** (`.jsonl`) | Each line parsed and displayed as numbered JSON entries. |
| **CSV** (`.csv`) | Rendered as an HTML table with headers from the first row. |
| **Other text** | Shown as plain preformatted text. |
| **Binary** | Not displayed. Download link provided. |

### JSON tree viewer

When viewing a `.json` file, click **Tree** to switch to the interactive tree view. Objects and arrays are collapsible -- click the arrow to expand or collapse. The header shows the type and count (e.g. `object{5}`, `array[12]`). Long strings are truncated to 200 characters.

Click **Raw** to switch back to the plain text view.

## File upload

Three ways to upload:

1. **Upload button** (toolbar) -- opens a file picker dialog. Select one or more files and click Upload.
2. **Drag and drop** -- drag files anywhere onto the workspace view. A drop overlay appears. Release to upload.
3. Both methods upload to the **current directory**.

Total upload size is limited to 10 MB per request.

## Context menu

Right-click any file or folder in the file table for a context menu:

| Action | Files | Folders |
|--------|:-----:|:-------:|
| **Open** | Yes | Yes |
| **Edit** | Yes | -- |
| **Download** | Yes | -- |
| **Delete** | Yes | Yes (if not protected) |

## Protected directories

Certain system directories cannot be deleted from the UI. They are marked with a **protected** badge in the file listing:

| Directory | Purpose |
|-----------|---------|
| `config.d` | Configuration files |
| `skills.d` | Skill definitions |
| `context` | Context files (heartbeat, etc.) |
| `docs` | Documentation |
| `logs` | Log files |
| `apps` | Marketplace apps |
| `sessions` | Session data |
| `tools.d` | Tool definitions |
| `agents/teams` | Agent team configs |

You can still create, edit, and delete **files inside** these directories. The protection only prevents deleting the directory itself.

Writes to `config.d/` and `skills.d/` are routed through their read-write mounts automatically -- you do not need to worry about mount details.

## Size limits

| Limit | Value |
|-------|-------|
| Max file size for viewing/editing | 1 MB |
| Max upload total per request | 10 MB |

Files larger than 1 MB show a "File too large to display" message with a download option.

## Spotlight search

The Control Center's spotlight search (the search bar in the navigation) can open files directly. When you search for a file name and select the result, the Workspace view opens with that file loaded in the viewer.

Similarly, links of the form `alf://files/path` or `alf://dirs/path` (from chat messages or apps) navigate directly to the file or directory in the Workspace.

## Hidden files

At the workspace root, dotfiles (files starting with `.`) and the `.git` directory are hidden from the listing. Inside subdirectories, dotfiles are visible.

## What's next

- [Chat](chat.md) -- ALF can create and modify workspace files during conversations
- [Config](config.md) -- edit ALF's configuration files in `config.d/`
- [Creating Skills](creating-skills.md) -- add skills to `skills.d/`
