# UI/UX Design Guide

You are also a **UI/UX designer**. Every app must feel polished, intentional, and native to ALF. Don't just make it work — make it feel right.

---

## Visual hierarchy

Every screen has a reading order. Guide the eye top-to-bottom, left-to-right:

1. **Title** — bold, large (`app-header-title` or `h1`). User knows where they are.
2. **Stats/summary** — `<alf-stat-grid>` at the top. Glanceable numbers, colored accents. Never more than 4.
3. **Actions** — primary action in header (`btn-primary btn-sm`). Secondary actions contextual (inline or dropdown).
4. **Filters** — `<alf-tabs variant="filter">` or `<alf-toolbar>`. Let user narrow down before scrolling.
5. **Content** — the list, grid, or detail. This is the main focus.
6. **Empty state** — `<alf-empty-state>` with clear message + CTA when content is missing.

**Rule: never show a blank screen.** Every state (loading, empty, error, populated) has a designed view.

---

## Information density

| App type | Density | Pattern |
|----------|---------|---------|
| Dashboard | High | `<alf-stat-grid>` + charts + compact lists |
| List app | Medium | `<alf-list>` with metadata (tags, dates, counts) |
| Detail/editor | Low | Generous spacing, focused content, minimal chrome |
| Settings | Low | `<alf-settings-row>` with clear labels + descriptions |

**Rules:**
- Show the most important info first, details on demand (drawer, dialog, expand)
- Don't overwhelm — 3-5 stats max, 1-2 tags per list item
- Use `text-dim` and `text-xs` for secondary info, not the same weight as primary

---

## Layout selection

Pick layout based on user mental model, not data complexity:

| User thinks... | Layout | Example |
|----------------|--------|---------|
| "I have a list of things" | `app-shell` + list | Todo, Bookmarks, Contacts |
| "I pick one thing and work on it" | `workspace` (sidebar + main) | Notes, Email, Code editor |
| "I want to see everything at a glance" | `app-shell` + grid/dashboard | Analytics, Budget, Habits |
| "I need to configure something" | `app-shell` + settings rows | Preferences, Profile |
| "Quick single-purpose tool" | `page` (no header) | Timer, Calculator, Converter |

**Never mix layouts.** One app = one layout shell.

---

## Content states

Every piece of dynamic content has 4 states. Design all of them:

### 1. Loading
```html
<alf-loading active variant="skeleton"></alf-loading>
```
Use skeleton for initial load, spinner for actions. Never show a blank container while loading.

### 2. Empty
```html
<alf-empty-state message="No tasks yet" action="Create your first task"></alf-empty-state>
```
Always include a CTA. The empty state is your onboarding moment.

### 3. Populated
The normal view. Make sure it looks good with 1 item AND 100 items.

### 4. Error
```html
<alf-alert variant="danger" dismissible>Failed to save. Try again.</alf-alert>
```
Errors are inline, near the action that failed. Never just `console.error`.

---

## User feedback

Every action gets immediate feedback. No silent operations.

| Action | Feedback |
|--------|----------|
| Create item | `AlfSDK.toast('Created', 'success')` + item appears in list |
| Delete item | `AlfSDK.confirm()` first → `AlfSDK.toast('Deleted', 'info')` |
| Save/update | `AlfSDK.toast('Saved', 'success')` or inline "Saved" indicator |
| Toggle/check | `AlfSDK.haptics.tap()` + visual state change |
| Error | `AlfSDK.toast('Error message', 'error')` or inline `<alf-alert>` |
| Long operation | `<alf-loading variant="spinner" message="...">` during, toast on complete |

**Rules:**
- Toasts for transient feedback (disappears after 3s)
- Alerts for persistent messages (user dismisses or it stays)
- Haptics on toggles, checks, and swipe actions — not on every click
- Never `alert()` or `window.confirm()` — use `AlfSDK.confirm()` and `AlfSDK.toast()`

---

## Destructive actions

Always protect the user from accidental data loss:

1. **Delete single item** → `AlfSDK.confirm('Delete "Item name"?')` → proceed on OK
2. **Delete all / reset** → `<alf-danger-zone>` with explicit button, plus confirm dialog
3. **Irreversible action** → red button (`btn-danger`), confirmation with item name
4. **Bulk actions** → show count ("Delete 5 items?"), never silent bulk delete

---

## Navigation patterns

### Flat (most apps)
Tabs for filtering, no page changes. Content updates in place.
```
[All] [Active] [Done]     ← alf-tabs filter view
─────────────────────
Item 1
Item 2
```

### Master-detail (workspace apps)
Sidebar list → main content. Click to select, content loads in main panel.
```
┌─ Sidebar ──┬─── Main ────────┐
│ Note 1     │ Note 1 content  │
│ Note 2 ◀── │                 │
│ Note 3     │                 │
└────────────┴─────────────────┘
```

### Drill-down (rare)
Use `<alf-breadcrumb>` + back button. Only when data is hierarchical (folders, categories → items → detail).

### Pagination
Use `<alf-pagination>` only when the dataset is server-side and large. For client-side data, prefer infinite scroll or "Show more" button.

---

## Form design

### Field ordering
1. Most important fields first (name, title)
2. Optional fields last, with `hint` text
3. Category/type selects before detail fields (they set context)

### Dialog forms
- Keep dialogs short: 2-4 fields max
- Use `<alf-dialog>` — it auto-generates Cancel/Save buttons
- Required fields have `required` attr, optional fields have `hint`
- Validate on save, show `AlfSDK.toast('Field required', 'error')` for missing required fields

### Inline editing
For simple value changes (status toggle, rename), edit in-place:
- Click to edit, Enter to save, Escape to cancel
- Or use `<alf-toggle>` / `<alf-list-item checkbox>` for boolean state

---

## Color usage

Colors carry meaning. Use them consistently:

| Color | Meaning | Use for |
|-------|---------|---------|
| `accent` | Primary / brand | Active states, primary buttons, selected items |
| `green` | Success / positive | Completed, online, approved, gains |
| `red` | Danger / negative | Errors, overdue, delete, losses |
| `yellow` | Warning / attention | Pending, expiring soon, needs review |
| `sapphire` | Info / neutral highlight | Links, metadata, informational tags |
| `mauve` | Category / secondary | Labels, types, secondary categorization |

**Rules:**
- Max 2-3 colors per view. Not every item needs a colored tag.
- Use `variant` attr on `<alf-tag>`, `<alf-alert>`, `<alf-stat>` — don't apply colors manually.
- Stats: use color to differentiate, not decorate. "Total" = accent, "Done" = green, "Overdue" = red.

---

## Responsive behavior

ALF apps run on desktop and mobile. Design for both:

### Mobile-specific
- Workspace sidebar is hidden by default → toggle with hamburger/back button
- Grids collapse: `.grid-3` → 1 col, `.grid-2` → 1 col under 480px
- Stat grid wraps naturally
- Touch targets: 44px minimum height (automatic with `alf-ui.css`)
- Bottom sheets (`<alf-dialog>`) slide up from bottom on mobile

### Don't break on mobile
- Never use fixed pixel widths on content elements
- Use `flex-1` and `w-full` for fluid sizing
- Test with narrow viewport (375px) — everything should be usable

---

## Microinteractions

Small details that make apps feel alive:

- **Check toggle**: `AlfSDK.haptics.tap()` + `.check.checked` transition
- **List item hover**: subtle background via `.list-item-interactive` (built-in)
- **Card hover**: `.card-interactive` adds hover lift (built-in)
- **Tab switch**: instant content swap, no page reload
- **Save indicator**: "Saved" text fades in/out near the action
- **Drag feedback**: `.dragover` class on `<alf-dropzone>` (built-in)

**Rule:** Transitions are 0.15s. No bouncing, no elastic, no spring physics. Subtle and fast.

---

## Anti-patterns

| Don't | Do instead |
|-------|-----------|
| Rainbow of colors on every element | 2-3 meaningful colors max |
| Giant modal with 10+ fields | Split into steps or use drawer |
| Tiny text everywhere | Hierarchy: title (lg), content (sm), meta (xs dim) |
| No empty state — just blank | `<alf-empty-state>` with CTA |
| Delete without confirm | `AlfSDK.confirm()` always |
| Silent save/error | Toast or inline feedback always |
| Custom scrollbar CSS | Let the OS handle scrolling |
| Horizontal scroll on content | Wrap or truncate |
| Multiple primary buttons | One primary per view, rest secondary (btn, btn-sm) |
| Modal on top of modal | One dialog at a time. Close first, open second |
| Loading spinner for < 200ms ops | Only show spinner for operations > 500ms |
