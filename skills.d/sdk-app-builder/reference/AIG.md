# ALF Interface Guidelines (AIG)

Design system for building consistent apps in the ALF workspace.
All apps share `alf-ui.css` (auto-injected into iframes) and theme CSS variables.

---

## Principles

1. **Consistency** — Every app should feel like it belongs in ALF. Same buttons, same spacing, same patterns.
2. **Theme-native** — Never hardcode colors. Use CSS variables so apps adapt to any palette.
3. **Mobile-first** — 44px minimum touch targets. Responsive grids collapse on small screens.
4. **Lightweight** — No frameworks. Vanilla JS + CSS classes. Fast load, small footprint.

---

## Theme setup

Every app must load three files in `<head>` to participate in ALF's theming system:

```html
<link rel="stylesheet" id="alf-theme" href="/static/theme-sage.css">
<script src="/static/theme-init.js"></script>
<script src="/static/alf-app-sdk.js"></script>
```

- **theme-*.css** — defines all `--bg`, `--text`, `--accent` etc. variables for that palette
- **theme-init.js** — reads `localStorage('alf-palette')` and swaps the CSS link to the user's chosen palette before first paint
- **alf-app-sdk.js** — listens for live theme switches from the parent SPA

### Available palettes (8)

`sage` (default), `studio`, `catppuccin`, `dracula`, `solarized`, `tokyo-night`, `github`, `nord`

Each palette defines light + dark variants via `prefers-color-scheme: dark` media query. The app doesn't need to handle dark mode — the CSS variables adapt automatically.

### Theme change handler

```js
AlfSDK.init({
  slug: 'my-app',
  onThemeChange: function(palette) {
    document.getElementById('alf-theme').href = '/static/theme-' + palette + '.css';
  }
});
```

This fires when the user switches themes in Settings. Without it, the app stays on the initial palette.

---

## Typography

### Font stack

Always set explicitly (Google Fonts blocked by CSP in iframes):

```css
body {
  font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
}
```

Monospace: `'JetBrains Mono', 'SF Mono', 'Consolas', monospace` (class: `.text-mono`)

### Scale

| Token | Size | Use for |
|-------|------|---------|
| `--font-xs` | 11px | Captions, hints, timestamps, meta items, tags |
| `--font-sm` | 13px | Body text, labels, buttons, inputs |
| `--font-md` | 15px | Emphasized body, large buttons |
| `--font-lg` | 18px | Section headings (h3) |
| `--font-xl` | 24px | Page titles (h1, h2) |

### Classes

`.text-xs`, `.text-sm`, `.text-md`, `.text-lg`, `.text-xl` — font size
`.text-dim` — secondary text color
`.text-bold` — font-weight 600
`.text-mono` — monospace font (IDs, versions, costs, code)
`.truncate` — overflow ellipsis

### Rules

- Body text: `--font-sm` (13px). Not 14px, not 15px.
- Labels: `--font-sm` with `font-weight: 500`.
- Hints / help text: `--font-xs` in `--text-dim`.
- Page title: one `h2` per view, `margin-bottom: var(--space-md)`.
- Section headings: `h3`.
- Monospace: use for IDs, hashes, versions, costs, code snippets.
- Never use `font-family: inherit` alone — always set the full stack.

---

## Icons (Lucide)

ALF uses [Lucide](https://lucide.dev) icons exclusively. In iframe apps, use inline SVGs (no icon fonts, no external URLs).

### Sizes

| Size | Use for | Example context |
|------|---------|-----------------|
| 12px | Meta items, inline with small text | `<Tag size={12} /> v1.2.0` |
| 14px | Buttons (btn-sm), toolbar actions | `<RefreshCw size={14} /> Refresh` |
| 16px | Section headings, expand/collapse chevrons | `<FolderOpen size={16} /> Category` |
| 20px | Card headers, app icons | App icon in marketplace card |
| 48px | Empty states | Centered illustration icon |

### Inline SVG pattern

```js
// Always use currentColor so icons inherit text color
var trashIcon = '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>';
```

### Rules

- Always `stroke="currentColor"` — icons inherit color from parent element.
- Never use emoji as icons (unless user explicitly asks).
- Use `width` and `height` attributes, not CSS sizing.
- Common icons: `RefreshCw` (refresh), `Trash2` (delete), `Plus` (add), `Pencil` (edit), `Search` (search), `ChevronRight`/`ChevronDown` (expand/collapse), `X` (close), `Check` (confirm).

---

## Colors

### Semantic usage

| Variable | Meaning | When to use |
|----------|---------|-------------|
| `--bg` | Page background | App body |
| `--bg-card` | Elevated surface | Cards, panels, toggle tracks |
| `--bg-input` | Input fields, hover states | Form elements, subtle buttons, tab hover |
| `--text` | Primary text | Headings, body |
| `--text-dim` | Secondary text | Labels, hints, placeholders, timestamps, meta |
| `--accent` | Primary action | CTA buttons, links, focus rings, active tabs |
| `--on-accent` | Text on accent | Button text on `--accent` background |
| `--border` | Dividers, borders | Card borders, separators, default button hover |
| `--radius` | Border radius | All rounded corners (default 8px) |
| `--green` | Success / positive | Confirmation, enabled status, success alerts |
| `--red` | Error / destructive | Delete buttons, error messages, failed status |
| `--yellow` | Warning / caution | Warning alerts, pending/disabled status |
| `--mauve` | Purple accent | Tags, managed/special items |
| `--sapphire` | Blue accent | Info alerts, tier badges, running status |
| `--pink` | Pink accent | Decorative, badges |
| `--teal` | Teal accent | Decorative, categories |
| `--peach` | Orange accent | Decorative, badges |
| `--lavender` | Light purple | Decorative, categories |

### Translucent backgrounds

For tinted backgrounds (alerts, status areas), use `color-mix()` — never hardcoded rgba:

```css
/* DO — adapts to any theme */
background: color-mix(in srgb, var(--red) 12%, var(--bg));
border-color: color-mix(in srgb, var(--red) 25%, transparent);

/* DON'T — hardcoded, breaks in other themes */
background: rgba(196, 57, 42, 0.1);
```

### Rules

- Never hardcode hex colors — always `var(--token)`.
- Destructive actions: `--red` (class `.btn-danger`).
- Primary CTA: `--accent` (class `.btn-primary`).
- Success confirmation: `--green` (class `.btn-success`).
- Default/secondary: `--bg-input`, no border (class `.btn`).
- Tags: translucent tinted backgrounds — `color-mix(in srgb, var(--color) 15%, var(--bg))` with colored text.
- Status alerts: use `.alert-success`, `.alert-danger`, `.alert-warning`, `.alert-info`.
- Status dots: `.dot` + `.dot-success` / `.dot-danger` / `.dot-warning`.
- Translucent tints: `color-mix(in srgb, var(--color) 12%, var(--bg))` for backgrounds.

---

## Spacing

### Scale

| Token | Value | Use for |
|-------|-------|---------|
| `--space-xs` | 4px | Tight gaps (tag padding, icon-to-text, label-to-input) |
| `--space-sm` | 8px | Default gap, compact padding, toolbar gap |
| `--space-md` | 16px | Card padding, form group margin, section gap, toolbar margin |
| `--space-lg` | 24px | Page padding, major section spacing |
| `--space-xl` | 32px | Empty states, large separations |

### Classes

**Gap**: `.gap-xs`, `.gap-sm`, `.gap-md`, `.gap-lg`, `.gap-xl`
**Padding**: `.p-xs` to `.p-xl`, `.px-sm` to `.px-lg`, `.py-sm` to `.py-lg`
**Margin**: `.mb-xs` to `.mb-lg`, `.mt-sm` to `.mt-lg`, `.m-0`

### Page padding token

Two tokens are defined in `alf-ui.css`:

- `--page-padding: 2.5rem` — base padding for all sides
- `--page-padding-top: calc(2.5rem + env(safe-area-inset-top))` — top padding that respects the notch/status bar

```css
/* Standard app body — notch-safe top, uniform sides */
body {
  padding: var(--page-padding-top) var(--page-padding) var(--page-padding);
}
```

Apps that are mobile-responsive should always use `--page-padding-top` for the top padding to avoid content hiding behind the notch on iOS.

### Rules

- Card padding: `--space-md` (class `.card` provides this).
- Gap between items in a list: `--space-sm`.
- Between sections: `--space-md` to `--space-lg`.
- Page body padding: `var(--page-padding)` (2.5rem). Never hardcode `2.5rem` directly.
- Toolbar gap: `--space-sm` (8px).
- Never use arbitrary values like `0.65rem` or `13px` for spacing. Use tokens.
- **Tokens vs exact values**: `--space-*` tokens are for general layout. For component-specific dimensions that must pixel-match across apps (e.g. score box padding, HUD height), use exact values documented in the component reference — don't force a token where the natural value doesn't align.

---

## Shadows

| Token | Use for |
|-------|---------|
| `--shadow-sm` | Subtle elevation (dropdowns, tooltips) |
| `--shadow-md` | Cards that need emphasis, floating panels |
| `--shadow-lg` | Modals, sheets, overlays |

ALF uses a **borderless** design — cards and buttons rely on background contrast, not borders. Use shadows sparingly for overlays and floating elements only.

---

## Animations & transitions

### Standard durations

| Duration | Use for | Example |
|----------|---------|---------|
| `0.12s` | List item hover | `transition: background 0.12s` |
| `0.15s` | Button hover, border, color changes | `transition: background 0.15s` |
| `0.2s` | Transform, toggle switch, larger motions | `transition: transform 0.2s` |

### Easing

- Default `ease` for most transitions (CSS default when unspecified).
- `linear` only for continuous rotation (spinner).
- Never use `ease-in-out` — it's not used anywhere in the CC.

### Animation classes

| Class | Effect | Use for |
|-------|--------|---------|
| `.spin` | 360deg rotation, 1s, linear, infinite | Loading icons (RefreshCw, Loader2) |
| `.pulse` | Opacity 1→0.5→1, 1.5s, infinite | Running/active status badges |
| `.fade-in` | Opacity 0→1, 0.2s | Newly appeared elements |

### Rules

- Hover transitions: always `0.15s`. Never 0.3s, never 0.1s.
- Toggle switch: `0.2s` for both track and thumb.
- Spinners: add class `.spin` to any Lucide icon to make it rotate.
- No animation on initial page load — only on state changes.
- Respect `prefers-reduced-motion` for essential animations only (the CSS classes handle this).

---

## Components

All components are available via `alf-ui.css` classes. No JS required.
**Design philosophy**: borderless surfaces. Cards, buttons, alerts, and tags rely on background contrast — not `border: 1px solid`.

### Buttons

Borderless — `bg-input` background, no border. Variants use solid accent colors.

```html
<button class="btn">Default</button>
<button class="btn btn-primary">Primary</button>
<button class="btn btn-danger">Delete</button>
<button class="btn btn-success">Confirm</button>
<button class="btn btn-ghost">Ghost</button>
<button class="btn btn-sm">Small</button>
<button class="btn btn-lg">Large</button>
<button class="btn btn-block">Full Width</button>
<button class="btn-icon"><!-- SVG icon --></button>
<button class="btn-icon-accent"><!-- SVG icon --></button>
```

- `.btn-icon-accent` — square 40x40 accent-filled icon button (e.g. add button next to input)

Button with icon:
```html
<button class="btn btn-sm">
  <!-- 14px Lucide SVG --> Refresh
</button>
<button class="btn btn-primary btn-sm">
  <!-- 14px Lucide SVG --> Add Item
</button>
```

Button group:
```html
<div class="btn-group">
  <button class="btn">Cancel</button>
  <button class="btn btn-primary">Save</button>
</div>
```

Toggle button group (e.g. speed selector):
```html
<div class="btn-group">
  <button class="btn btn-sm active" onclick="setSpeed(1)">1x</button>
  <button class="btn btn-sm" onclick="setSpeed(2)">2x</button>
  <button class="btn btn-sm" onclick="setSpeed(5)">5x</button>
</div>
```
Add/remove `.active` class via JS. The `.btn.active` style uses `--accent` background.

### Cards

Borderless — `bg-card` surface stands out from `bg` via contrast alone.

```html
<div class="card">
  <h3>Title</h3>
  <p class="text-dim">Description</p>
</div>

<!-- Card with header row (title + actions) -->
<div class="card">
  <div class="card-header">
    <h3>Section</h3>
    <button class="btn btn-sm">Action</button>
  </div>
  <!-- content -->
</div>

<div class="card-interactive">Clickable card (hover → bg-input)</div>
```

### Card group

Continuous surface with light internal separators. Use to wrap lists of items.
Separators are `border 40% opacity` — ultra-light, not full `--border`.

```html
<div class="section-label">Services <span class="count">3</span></div>
<div class="card-group">
  <div class="list-item-interactive">
    <span class="dot dot-success"></span>
    <span class="flex-1">vault-server</span>
    <span class="tag tag-success">Running</span>
  </div>
  <div class="list-item-interactive">
    <span class="dot dot-danger"></span>
    <span class="flex-1">sandbox-worker</span>
    <span class="tag tag-danger">Stopped</span>
  </div>
</div>
```

**Prefer `.card-group`** over `.card > .list` for list views — it gives a cleaner, tighter appearance.

### Input row (input + button)

Standard pattern for text input with an action button (e.g. add todo, send message):

```html
<div class="input-row">
  <input class="input" type="text" placeholder="Nouvelle tâche...">
  <button class="btn btn-primary">Ajouter</button>
</div>
```

`.input-row` provides `display: flex` + `gap: 8px` + `margin-bottom: 16px`. The input auto-expands (`flex: 1`). No need to add spacing manually.

### Custom checkbox

**NEVER use emoji checkboxes** (✅, ☑️, ⬜). Use the `.check` CSS class — a 20px circle that fills with accent color when checked.

```html
<!-- Unchecked -->
<div class="check" onclick="this.classList.toggle('checked')"></div>

<!-- Checked -->
<div class="check checked"></div>
```

`.check` has a built-in `margin-right: 12px` — text never touches the circle.

In list items:

```html
<div class="list-item-interactive">
  <div class="check"></div>
  <span class="flex-1">Task text</span>
</div>
```

No extra gap or margin needed — `.check` handles spacing, `.list-item-interactive` provides padding (11px 14px).

### Forms

```html
<div class="form-group">
  <label class="form-label">Name</label>
  <input class="input" type="text" placeholder="Enter name">
  <p class="form-hint">This will be displayed publicly.</p>
</div>

<div class="form-group">
  <label class="form-label">Category</label>
  <select class="select">
    <option>Option A</option>
    <option>Option B</option>
  </select>
</div>

<div class="form-group">
  <label class="form-label">Description</label>
  <textarea class="textarea" rows="3"></textarea>
</div>

<!-- Side by side -->
<div class="form-row">
  <div class="form-group">
    <label class="form-label">First</label>
    <input class="input" type="text">
  </div>
  <div class="form-group">
    <label class="form-label">Last</label>
    <input class="input" type="text">
  </div>
</div>
```

### Toggle switch

```html
<label class="toggle">
  <input type="checkbox">
  <span class="toggle-track"></span>
  Enable feature
</label>
```

### Toolbar

Standard pattern for view-level controls:

```html
<div class="toolbar">
  <div class="search-box">
    <!-- 14px Search icon SVG -->
    <input type="text" placeholder="Search...">
  </div>
  <button class="btn btn-ghost btn-sm"><!-- icon --> Expand</button>
  <button class="btn btn-primary btn-sm"><!-- icon --> Add</button>
</div>
```

### Search box

```html
<div class="search-box">
  <!-- 14px Search icon SVG -->
  <input type="text" placeholder="Search...">
</div>
```

### Tab bar (view navigation)

Use for switching between views/pages (Dashboard, Settings, History):

```html
<div class="tab-bar">
  <button class="tab-item active">Dashboard</button>
  <button class="tab-item">Settings</button>
  <button class="tab-item">History</button>
</div>
```

### Filter tabs (segmented control)

iOS-style segmented control for in-page filtering. Active tab is elevated with `bg-card` + subtle shadow.

```html
<div class="filter-tabs">
  <button class="tab active">All</button>
  <button class="tab">Active</button>
  <button class="tab">Archived</button>
</div>
```

**When to use which:**
- **Tab bar** (underline): switching between different views/pages
- **Filter tabs** (segmented): filtering/toggling within the same view

### Data table

For data-heavy layouts with rows and columns:

```html
<table class="data-table">
  <thead>
    <tr><th>Name</th><th class="num">Amount</th><th class="num">%</th></tr>
  </thead>
  <tbody>
    <tr><td>Item A</td><td class="num">1 234 €</td><td class="num positive">+5.2%</td></tr>
    <tr><td>Item B</td><td class="num">567 €</td><td class="num negative">-2.1%</td></tr>
    <tr><td>Item C</td><td class="num dim">0 €</td><td class="num dim">0%</td></tr>
  </tbody>
</table>
```

Utility classes: `.num` (right-align, tabular-nums), `.dim`, `.positive`, `.negative`.

### Stat grid (KPI / metrics)

Fused 4-column grid with 1px borders between items. Each cell has a colored accent bar at the top for category identification. Collapses to 2 columns on mobile (≤600px).

```html
<div class="stat-grid">
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--sapphire)"></div>
    <div class="stat-value">15 040 €</div>
    <div class="stat-label">Actions</div>
    <div class="stat-sub">5.1%</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--green)"></div>
    <div class="stat-value">1 480 €</div>
    <div class="stat-label">Crypto</div>
    <div class="stat-sub">0.5%</div>
  </div>
  <!-- more items... -->
</div>
```

- `.stat-bar` takes its color via inline `style="background:var(--sapphire)"` — each item has its own accent color.
- **Do NOT** nest `.card` inside `.card` for KPI grids — use `.stat-grid` instead.
- **Do NOT** scope `.stat-value` / `.stat-label` / `.stat-sub` under `.stat-item` — they are top-level classes.

### Lists

List items no longer have built-in borders — they get separators from `.card-group` parent.

```html
<!-- Standalone list (no separators) -->
<ul class="list">
  <li class="list-item">Static item</li>
  <li class="list-item-interactive" onclick="...">Clickable item</li>
</ul>

<!-- List inside card-group (auto-separators) -->
<div class="card-group">
  <div class="list-item-interactive">Item A</div>
  <div class="list-item-interactive">Item B</div>
</div>
```

### Tags

Translucent tinted backgrounds — text carries the color, background is a 15% tint.

```html
<span class="tag">Default</span>
<span class="tag tag-accent">Active</span>
<span class="tag tag-success">Online</span>
<span class="tag tag-danger">Error</span>
<span class="tag tag-warning">Pending</span>
<span class="tag tag-mauve">Managed</span>
<span class="tag tag-sapphire">Info</span>
```

### Meta items (icon + text)

```html
<span class="meta"><!-- 12px icon SVG --> v1.2.0</span>
<span class="meta"><!-- 12px icon SVG --> 3 items</span>
```

### Alerts

```html
<div class="alert alert-success">Operation completed.</div>
<div class="alert alert-danger">Something went wrong.</div>
<div class="alert alert-warning">Check your input.</div>
<div class="alert alert-info">Tip: you can also...</div>
```

### Empty state

```html
<div class="empty-state">
  <!-- 48px Lucide SVG icon -->
  <p>No items yet. Create your first one.</p>
  <button class="btn btn-primary">Create Item</button>
</div>
```

### Loading

```html
<div class="loading-state">
  <div class="spinner"></div>
  Loading...
</div>
```

### Dividers

```html
<hr class="divider">
<hr class="divider-sm">
```

### Section labels

Uppercase dim label with optional count. Use above `.card-group` to group items.

```html
<div class="section-label">In progress <span class="count">4</span></div>
<div class="card-group">...</div>
```

`.section-title` is an alias for `.section-label` (same styles).

### Progress bar

Thin 4px track with colored fill. Use for completion, quotas, storage.

```html
<div class="progress-bar">
  <div class="progress-track">
    <div class="progress-fill" style="width: 72%"></div>
  </div>
  <span class="progress-label">72%</span>
</div>
```

Override fill color with inline style: `style="width:92%;background:var(--red)"` for quota warnings.

### Status dots

```html
<span class="dot dot-success"></span> Connected
<span class="dot dot-danger"></span> Offline
<span class="dot dot-warning"></span> Pending
```

---

## Layout patterns

### Standard app page

```html
<body>
  <h2>App Name</h2>

  <div class="card mb-md">
    <div class="card-header">
      <h3>Section</h3>
      <button class="btn btn-sm">Action</button>
    </div>
    <!-- content -->
  </div>

  <div class="card">
    <h3 class="mb-sm">Another Section</h3>
    <!-- content -->
  </div>
</body>
```

Body style:
```css
body {
  padding: var(--page-padding-top, 2.5rem) var(--page-padding, 2.5rem) var(--page-padding, 2.5rem);
  max-width: 760px;
  margin: 0 auto;
  font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  background: var(--bg);
  color: var(--text);
  line-height: 1.5;
}
h2 { margin-bottom: var(--space-md, 16px); }
```

### List view with toolbar

```html
<body>
  <h2>Items</h2>

  <div class="toolbar">
    <div class="search-box">
      <!-- Search icon --> <input type="text" placeholder="Search...">
    </div>
    <div style="flex:1"></div>
    <div class="filter-tabs" style="margin-bottom:0">
      <button class="tab active">All</button>
      <button class="tab">Active</button>
      <button class="tab">Archived</button>
    </div>
    <button class="btn btn-primary btn-sm"><!-- Plus icon --> Add</button>
  </div>

  <div class="section-label">Active <span class="count">3</span></div>
  <div class="card-group" id="items">
    <!-- populated by JS with list-item-interactive -->
  </div>

  <div class="empty-state" id="empty" style="display:none">
    <p>No items match the current filter.</p>
  </div>
</body>
```

### Grid layout

```html
<div class="grid-2">
  <div class="card">Card 1</div>
  <div class="card">Card 2</div>
  <div class="card">Card 3</div>
  <div class="card">Card 4</div>
</div>
```

---

## Sheets (bottom-sheet modals)

Sheets are rendered by the parent SPA via `AlfSDK.sheet()`. Content is sanitized HTML.

### Structure

```js
AlfSDK.sheet(
  '<h3>Title</h3>' +
  '<p class="text-dim">Description of what this does.</p>' +
  '<hr class="divider">' +
  '<div class="form-group">' +
    '<label class="form-label">Field</label>' +
    '<input class="input" name="field" placeholder="...">' +
  '</div>',
  [
    { label: 'Cancel', callback: function() { AlfSDK.closeSheet(); } },
    { label: 'Save', style: 'background:var(--accent);color:var(--on-accent)', callback: function(p) {
      save(p.field);
      AlfSDK.closeSheet();
    }}
  ]
);
```

### Rules

- Always start with an `<h3>` title.
- Description in `<p>` with `text-dim` color.
- Use `<hr class="divider">` to separate title from content.
- Form inputs must have `name` attribute (auto-collected as callback params).
- Destructive action button: `style="background:var(--red);color:#fff"`.
- Primary action button: `style="background:var(--accent);color:var(--on-accent)"`.

---

## Mobile considerations

- **Touch targets**: 44px minimum height on mobile (automatic via `alf-ui.css`).
- **Grids**: `.grid-2` and `.grid-3` collapse to single column under 480px.
- **Form rows**: `.form-row` stacks vertically under 480px.
- **Safe area**: Use `env(safe-area-inset-bottom)` for bottom-fixed elements.
- **No hover-only interactions**: Always provide tap alternatives.

---

## Do / Don't

### Buttons

- DO: `<button class="btn btn-primary">Save</button>`
- DON'T: `<button style="padding:8px 16px;background:#5a8f5a;color:white;border:none;border-radius:8px">Save</button>`

### Spacing

- DO: `<div class="card mb-md">` or `padding: var(--space-md)`
- DON'T: `padding: 0.65rem` or `margin-bottom: 13px`

### Colors

- DO: `color: var(--text-dim)` or `class="text-dim"`
- DON'T: `color: #6b7b6b` or `color: gray`

### Translucent backgrounds

- DO: `background: color-mix(in srgb, var(--red) 12%, var(--bg))`
- DON'T: `background: rgba(196, 57, 42, 0.1)`

### Icons & checkboxes

- DO: inline SVG with `stroke="currentColor"`, size 14px for buttons
- DO: `<div class="check"></div>` for checkboxes
- DON'T: emoji icons (✅, ☑️, ⬜), external icon fonts, `<img>` tags for icons
- DON'T: native `<input type="checkbox">` without `.toggle` wrapper

### Input rows

- DO: `<div class="input-row"><input class="input"><button class="btn btn-primary">Add</button></div>`
- DON'T: `<input style="..."><button style="...">` without flex container or gap

### Animations

- DO: `transition: background 0.15s` on hover states
- DON'T: `transition: all 0.3s ease-in-out`

### Forms

- DO: `<div class="form-group"><label class="form-label">Name</label><input class="input"></div>`
- DON'T: `<label style="display:block;font-size:12px">Name</label><input style="width:100%;padding:8px;...">`

### Empty states

- DO: `<div class="empty-state"><p>No items yet.</p><button class="btn btn-primary">Create</button></div>`
- DON'T: `<p style="text-align:center;color:gray">Nothing here</p>`

### Lists

- DO: `<div class="card-group"><div class="list-item-interactive">Item</div></div>`
- DON'T: `<div style="padding:8px;border-bottom:1px solid #333">Item</div>`

### Cards & surfaces

- DO: `<div class="card">` (borderless, bg-card background)
- DON'T: `<div style="border:1px solid var(--border);border-radius:8px;padding:16px">`

### Tags

- DO: `<span class="tag tag-success">Online</span>` (translucent tint)
- DON'T: `<span style="background:var(--green);color:#fff;padding:2px 8px">Online</span>`

---

## Checklist for new apps

- [ ] Theme setup: `theme-*.css` + `theme-init.js` + `alf-app-sdk.js` in `<head>`
- [ ] `AlfSDK.init()` with `onThemeChange` handler
- [ ] Body has explicit `font-family`, `background: var(--bg)`, `color: var(--text)`
- [ ] All colors use CSS variables, zero hardcoded hex values
- [ ] Translucent backgrounds use `color-mix()`, not `rgba()`
- [ ] All spacing uses `--space-*` tokens or `alf-ui.css` classes
- [ ] Buttons use `.btn` + variant classes, **no borders**
- [ ] Forms use `.form-group` + `.form-label` + `.input`
- [ ] Cards use `.card` (borderless) or `.card-group` for lists
- [ ] Tags use `.tag` + `.tag-*` (translucent tinted, not opaque)
- [ ] Lists use `.card-group` > `.list-item-interactive` pattern
- [ ] Icons are inline Lucide SVGs with `currentColor`, sized 12/14/16/20px
- [ ] Empty states use `.empty-state`
- [ ] Loading states use `.loading-state` + `.spinner`
- [ ] Hover transitions are `0.15s`, not arbitrary values
- [ ] Touch targets are 44px+ on mobile
- [ ] No `border: 1px solid` on cards, buttons, tags, or alerts
- [ ] No emoji checkboxes — use `.check` class
- [ ] Input + button combos use `.input-row` (flex + gap)
- [ ] No inline styles for things covered by `alf-ui.css` classes
