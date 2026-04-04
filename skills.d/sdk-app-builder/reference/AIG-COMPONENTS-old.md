# AIG Component Reference

Full HTML templates for all `alf-ui.css` components and layout patterns.
Read this file when you need to copy specific component markup.

> See `AIG.md` for design rules, tokens, and the Do/Don't checklist.

---

## Components

All components are available via `alf-ui.css` classes. No JS required.
**Design philosophy**: borderless surfaces. Cards, buttons, alerts, and tags rely on background contrast — not `border: 1px solid`.

> **IMPORTANT**: See `reference/components.html` for a visual gallery of every component with copy-paste HTML.
> When building an app, copy the exact HTML patterns from `components.html` — do NOT invent custom CSS.

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

### Content card (feed item / post idea / article)

Use a plain `.card` for content items that have a title, tag, body, hook, and source. No custom borders.

```html
<div class="card">
  <div class="mb-xs">
    <span class="text-bold text-md">Card title here</span>
  </div>
  <div class="mb-sm">
    <span class="tag tag-accent">Thread</span>
  </div>
  <p class="text-sm mb-sm">Body text — main content of the item.</p>
  <p class="text-sm text-dim mb-sm" style="font-style: italic;">Hook — one-line angle or insight.</p>
  <div class="text-xs text-dim">Source : original article title</div>
</div>
```

**Common mistakes to avoid:**

| Wrong | Correct |
|-------|---------|
| `background: var(--accent); color: var(--on-accent)` on tags | `.tag.tag-accent` (translucent 15%) |
| `border-left: 3px solid var(--accent)` on every card | No border — only `.accordion details[open]` gets this |
| Uppercase section header with inline CSS | `.text-xs.text-bold` with `letter-spacing` |
| Custom italic class for hook | `<p class="text-sm text-dim" style="font-style:italic">` |
| Hardcoded hex/rgba colors | Always use CSS variables |

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

### Footer stats

Bottom status line for counts. Use below lists, progress bars, etc.

```html
<div class="footer-stats">2 / 5 terminées</div>
```

### Danger zone

Red-tinted section for destructive actions. Headings auto-color red.

```html
<div class="danger-zone">
  <h4>Supprimer le compte</h4>
  <p class="text-sm text-dim">Cette action est irréversible.</p>
  <button class="btn btn-danger mt-sm">Supprimer</button>
</div>
```

### Keyboard shortcuts

```html
Press <span class="kbd">⌘K</span> to search
```

### Skeleton loading

Placeholder shimmer for async content. Set dimensions on the element.

```html
<!-- Text placeholder -->
<div class="skeleton-text" style="width: 60%"></div>
<div class="skeleton-text" style="width: 80%"></div>

<!-- Block placeholder -->
<div class="skeleton" style="width: 100%; height: 120px"></div>

<!-- Circle placeholder (avatar) -->
<div class="skeleton-circle"></div>
```

### Backdrop

Full-screen overlay for custom modals. Use `AlfSDK.sheet()` when possible — this is for cases where you need a fully custom modal.

```html
<div class="backdrop" onclick="closeModal()"></div>
<div class="card" style="position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);z-index:101;width:400px">
  <!-- modal content -->
</div>
```

### Done state

Strikethrough + dim for completed items. Works on any element.

```html
<span class="done">Completed task text</span>
```

### Spacer

Pushes siblings apart in flex containers. Replaces `<div style="flex:1"></div>`.

```html
<div class="toolbar">
  <h3>Title</h3>
  <div class="spacer"></div>
  <button class="btn btn-sm">Action</button>
</div>
```

### Avatar

Circle with initials or image. Sizes: default 32px, `-sm` 24px, `-lg` 40px.

```html
<!-- Initials -->
<div class="avatar">AL</div>
<div class="avatar avatar-sm">J</div>
<div class="avatar avatar-lg">MR</div>

<!-- Image -->
<div class="avatar"><img src="/photo.jpg" alt=""></div>

<!-- Stack (overlapping) -->
<div class="avatar-stack">
  <div class="avatar">A</div>
  <div class="avatar">B</div>
  <div class="avatar">C</div>
</div>
```

Use in: chat messages, team lists, contact cards, activity feeds.

### Key-value row

Horizontal label: value pair with auto-separators between rows.

```html
<div class="card">
  <div class="kv-row">
    <span class="kv-label">Status</span>
    <span class="kv-value"><span class="tag tag-success">Active</span></span>
  </div>
  <div class="kv-row">
    <span class="kv-label">Version</span>
    <span class="kv-value text-mono">1.2.0</span>
  </div>
  <div class="kv-row">
    <span class="kv-label">Created</span>
    <span class="kv-value">Mar 31, 2026</span>
  </div>
</div>
```

Use in: detail views, profile pages, settings, info panels.

### Line clamp

Multi-line truncation with ellipsis. Use on descriptions, previews.

```html
<p class="line-clamp-2">Long description that may span multiple lines...</p>
<p class="line-clamp-3">Even longer text with 3 lines max...</p>
```

### Change indicator

Colored percentage change with directional arrows. Auto-inserted via CSS `::before`.

```html
<span class="change-up">+5.2%</span>
<span class="change-down">-1.8%</span>
<span class="change-flat">0%</span>
```

Pair with `.data-table .num` or `.stat-sub` for financial/dashboard apps.

### Chip (removable tag)

Tag-like element with a close button. Use for active filters, tag inputs.

```html
<div class="chip">
  Finance
  <button class="chip-close" onclick="removeFilter(this)">
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
  </button>
</div>

<div class="chip chip-accent">Active <button class="chip-close">...</button></div>
<div class="chip chip-danger">Error <button class="chip-close">...</button></div>
```

### Sticky header

Sticks to top on scroll. Use on toolbars or section headers in long lists.

```html
<div class="toolbar sticky">
  <h3>Results</h3>
  <div class="spacer"></div>
  <button class="btn btn-sm">Export</button>
</div>
```

---

### Accordion

Collapsible sections using native `<details>/<summary>`. No JS needed.

The accordion has a full border and each open item gets an accent left border to differentiate sections visually.

```html
<div class="accordion">
  <details>
    <summary>Section title</summary>
    <div class="accordion-content">Content here</div>
  </details>
  <details open>
    <summary>Open by default</summary>
    <div class="accordion-content">This section starts open</div>
  </details>
</div>
```

To render **markdown / rich text** inside an accordion, combine `.accordion-content` with `.prose`:

```html
<details open>
  <summary>With markdown content</summary>
  <div class="accordion-content prose">
    <h3>Title</h3>
    <p>Paragraph with <strong>bold</strong> and <code>inline code</code>.</p>
    <ul>
      <li>Item one</li>
      <li>Item two</li>
    </ul>
  </div>
</details>
```

---

### Prose / Markdown

Use `.prose` on any container that renders markdown or rich text output. It styles all standard HTML elements with consistent typography and spacing.

```html
<div class="prose">
  <h1>Page Title</h1>
  <h2>Section</h2>
  <h3>Subsection</h3>
  <h4>Label</h4>
  <p>Body text with <strong>bold</strong>, <em>italic</em>, and <a href="#">links</a>.</p>
  <ul>
    <li>List item</li>
  </ul>
  <ol>
    <li>Ordered step</li>
  </ol>
  <blockquote>Note or callout — gets accent border and tinted background.</blockquote>
  <pre><code>// Code block
const x = 1</code></pre>
  <hr>
  <table>
    <thead><tr><th>Name</th><th>Value</th></tr></thead>
    <tbody><tr><td>foo</td><td>bar</td></tr></tbody>
  </table>
</div>
```

**Heading hierarchy in `.prose`:**

| Element | Size | Style |
|---------|------|-------|
| `h1` | 1.75rem | Bold, large title |
| `h2` | 1.35rem | Semi-bold + bottom border |
| `h3` | 1.1rem | Semi-bold |
| `h4` | 13px | Uppercase label, `text-dim` |
| `h5`, `h6` | 13px | Dim secondary label |

**Rules:**
- Use `.prose` for any rendered markdown — app descriptions, README content, AI responses
- `.prose` + `.accordion-content` work together — just add both classes
- Never re-implement heading styles manually — always use `.prose`

### Tooltip

CSS-only tooltip via `data-tooltip` attribute:

```html
<button class="btn" data-tooltip="Save changes">Save</button>
<button class="btn-icon" data-tooltip="Settings"><!-- icon --></button>
```

### Dropdown menu

Focus-within triggered menu:

```html
<div class="dropdown">
  <button class="btn-icon" tabindex="0"><!-- more icon --></button>
  <div class="dropdown-menu">
    <div class="dropdown-label">Actions</div>
    <button class="dropdown-item"><!-- icon --> Edit</button>
    <button class="dropdown-item"><!-- icon --> Duplicate</button>
    <div class="dropdown-separator"></div>
    <button class="dropdown-item dropdown-item-danger"><!-- icon --> Delete</button>
  </div>
</div>
```

### Breadcrumb

```html
<nav class="breadcrumb">
  <a href="#">Home</a>
  <span class="breadcrumb-sep">/</span>
  <a href="#">Projects</a>
  <span class="breadcrumb-sep">/</span>
  <span class="breadcrumb-current">Alpha</span>
</nav>
```

### Pagination

```html
<div class="pagination">
  <button class="page-btn" disabled><!-- chevron-left --></button>
  <button class="page-btn active">1</button>
  <button class="page-btn">2</button>
  <button class="page-btn">3</button>
  <span class="page-ellipsis">...</span>
  <button class="page-btn">12</button>
  <button class="page-btn"><!-- chevron-right --></button>
</div>
```

### Slider

```html
<input type="range" class="slider" min="0" max="100" value="72">
```

### Bar chart (vertical)

```html
<div class="bar-chart" style="height:100px">
  <div class="bar-chart-col">
    <span class="bar-chart-value">12</span>
    <div class="bar-chart-bar" style="height:40%"></div>
    <span class="bar-chart-label">Mon</span>
  </div>
  <!-- more columns... -->
</div>
```

Override bar color: `style="height:60%; background:var(--green)"`.

### Horizontal bar chart

```html
<div class="hbar-chart">
  <div class="hbar-row">
    <span class="hbar-label">Design</span>
    <div class="hbar-track"><div class="hbar-fill" style="width:85%"></div></div>
    <span class="hbar-value">85%</span>
  </div>
</div>
```

Override fill color: `style="width:62%; background:var(--sapphire)"`.

### Sparkline

Inline mini bars for tables/dashboard rows:

```html
<span class="sparkline">
  <span class="sparkline-bar" style="height:30%"></span>
  <span class="sparkline-bar" style="height:50%"></span>
  <span class="sparkline-bar" style="height:80%"></span>
  <!-- more bars... -->
</span>
```

### Ring chart

SVG donut/progress ring. `stroke-dasharray` = circumference (2πr), `stroke-dashoffset` = remaining.

```html
<div class="ring-chart">
  <svg viewBox="0 0 80 80">
    <circle class="ring-bg" cx="40" cy="40" r="34"/>
    <circle class="ring-fill" cx="40" cy="40" r="34"
      stroke-dasharray="213.6" stroke-dashoffset="55.5"/>
  </svg>
  <span class="ring-chart-value">74%</span>
</div>
<div class="ring-chart-label">Storage</div>
```

Formula: `stroke-dashoffset = 213.6 * (1 - percentage/100)`. Override color: `style="stroke:var(--green)"`.

### Carousel

Scroll-snap based, no JS for basic behavior:

```html
<!-- Card carousel -->
<div class="carousel carousel-cards">
  <div class="carousel-item"><div class="card">Card 1</div></div>
  <div class="carousel-item"><div class="card">Card 2</div></div>
</div>

<!-- Peek carousel (85% width, shows next slide) -->
<div class="carousel carousel-peek">...</div>

<!-- Full-width slides -->
<div class="carousel carousel-full">...</div>

<!-- Dots indicator -->
<div class="carousel-dots">
  <button class="carousel-dot active"></button>
  <button class="carousel-dot"></button>
</div>
```

### Dropzone / file upload

```html
<!-- Large drop area -->
<div class="dropzone">
  <!-- 40px upload icon -->
  <div class="dropzone-text">Drag & drop files here, or <span style="color:var(--accent)">browse</span></div>
  <div class="dropzone-hint">PNG, JPG, PDF up to 10MB</div>
</div>

<!-- Compact inline drop row -->
<div class="dropzone-compact">
  <!-- 16px upload icon --> Attach a file...
</div>
```

Add `.dragover` class via JS on `dragenter`/`dragleave` events.

### File item

```html
<div class="file-item">
  <div class="file-icon"><!-- 16px file icon --></div>
  <div class="file-info">
    <div class="file-name">document.pdf</div>
    <div class="file-meta">2.4 MB · Just now</div>
  </div>
  <button class="btn-icon"><!-- X icon --></button>
</div>
```

Override icon color: `style="background:color-mix(in srgb, var(--green) 12%, var(--bg)); color:var(--green)"`.

### Popover

```html
<div class="popover">
  <button class="btn btn-sm" tabindex="0">More info</button>
  <div class="popover-content">
    <p>Popover content here</p>
  </div>
</div>
```

---

## Layout patterns

### Standard app page

Use `.page` on `<body>` — it sets padding, max-width, font, bg, and color in one class. **No custom body CSS needed.**

```html
<body class="page">
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

**Do NOT write body CSS manually** — `.page` handles everything. Only override `max-width` if the app needs a wider layout.

### List view with toolbar

```html
<body class="page">
  <h2>Items</h2>

  <div class="toolbar">
    <div class="search-box">
      <!-- Search icon --> <input type="text" placeholder="Search...">
    </div>
    <div class="spacer"></div>
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

  <div class="footer-stats">3 / 10 items</div>
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

### App shell layout

For apps that need a sticky header, scrollable body, and optional footer. **Use instead of `.page` when you need header actions or a footer.**

```html
<body>
<div class="app-shell">
  <div class="app-header">
    <h1 class="app-header-title">My App</h1>
    <span class="spacer"></span>
    <div class="app-header-actions">
      <button class="btn-icon"><!-- search icon --></button>
      <button class="btn btn-primary btn-sm"><!-- plus icon --> Add</button>
    </div>
  </div>
  <div class="app-body">
    <!-- content here -->
  </div>
  <div class="app-footer">
    <span>3 items</span>
    <span class="spacer"></span>
    <span>Updated just now</span>
  </div>
</div>
</body>
```

Use `.app-body-wide` instead of `.app-body` for dashboards (no max-width).

### Workspace layout

Sidebar + main panel for complex apps (mail, notes, IDE-style):

```html
<body>
<div class="workspace">
  <div class="workspace-sidebar">
    <div class="workspace-sidebar-header"><!-- logo + title --></div>
    <div class="workspace-sidebar-body">
      <div class="sidebar-section">
        <div class="sidebar-section-title">Navigation</div>
        <nav class="sidebar-nav">
          <button class="sidebar-item active"><!-- icon --> Dashboard</button>
          <button class="sidebar-item"><!-- icon --> Documents</button>
        </nav>
      </div>
    </div>
    <div class="workspace-sidebar-footer">
      <button class="sidebar-item"><!-- icon --> Settings</button>
    </div>
  </div>
  <div class="workspace-main">
    <div class="workspace-main-header">
      <h3>Dashboard</h3>
      <span class="spacer"></span>
      <!-- toolbar actions -->
    </div>
    <div class="workspace-main-body">
      <!-- content -->
    </div>
  </div>
</div>
</body>
```

Add `.workspace-detail` as a third panel for 3-column layouts (mail reader, detail inspector).

Sidebar and detail panel auto-hide on mobile (≤768px).

### Settings page

```html
<body>
<div class="app-shell">
  <div class="app-header">
    <h1 class="app-header-title">Settings</h1>
  </div>
  <div class="app-body" style="max-width:640px">
    <div class="settings-section">
      <div class="section-label">General</div>
      <div class="card">
        <div class="settings-row">
          <div class="settings-row-info">
            <div class="settings-row-label">Notifications</div>
            <div class="settings-row-desc">Receive alerts when tasks complete</div>
          </div>
          <label class="toggle"><input type="checkbox" checked><span class="toggle-track"></span></label>
        </div>
        <div class="settings-row">
          <div class="settings-row-info">
            <div class="settings-row-label">Language</div>
            <div class="settings-row-desc">Display language</div>
          </div>
          <select class="select" style="width:140px"><option>English</option></select>
        </div>
      </div>
    </div>
    <div class="settings-section">
      <div class="section-label">Danger zone</div>
      <div class="danger-zone">
        <div class="settings-row" style="border:none;padding-top:0">
          <div class="settings-row-info">
            <div class="settings-row-label">Delete all data</div>
            <div class="settings-row-desc">This cannot be undone</div>
          </div>
          <button class="btn btn-danger btn-sm">Delete</button>
        </div>
      </div>
    </div>
  </div>
</div>
</body>
```
