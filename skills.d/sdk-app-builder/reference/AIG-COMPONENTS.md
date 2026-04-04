# AIG Component Reference — v2

Full HTML templates for all `alf-ui-v2.css` components. Copy-paste ready.

> See `AIG.md` for design rules, tokens, and the Do/Don't checklist.

---

## Design philosophy

**Bordered surfaces**. Cards, buttons, inputs, and tags use `border: 1px solid var(--border)` with `box-shadow: var(--shadow-sm)`. Focus states use a ring: `box-shadow: var(--ring)`.

This replaces v1's borderless/contrast-only approach.

---

## Components

### Buttons

Bordered with subtle shadow. Variants override background + border-color.

```html
<button class="btn">Default</button>
<button class="btn btn-primary">Primary</button>
<button class="btn btn-danger">Delete</button>
<button class="btn btn-success">Confirm</button>
<button class="btn btn-ghost">Ghost</button>
<button class="btn btn-outline">Outline</button>
<button class="btn btn-outline-danger">Outline Danger</button>
<button class="btn btn-sm">Small</button>
<button class="btn btn-lg">Large</button>
<button class="btn btn-block">Full Width</button>
<button class="btn" disabled>Disabled</button>
```

Icon button (no border, dim color, hover reveals bg):
```html
<button class="btn-icon"><!-- 16px SVG icon --></button>
```

Button with icon:
```html
<button class="btn btn-sm">
  <!-- 14px Lucide SVG --> Refresh
</button>
```

Button group:
```html
<div class="btn-group">
  <button class="btn">Left</button>
  <button class="btn">Center</button>
  <button class="btn">Right</button>
</div>
```

Toggle button (active state):
```html
<div class="btn-group">
  <button class="btn btn-sm active">1x</button>
  <button class="btn btn-sm">2x</button>
  <button class="btn btn-sm">5x</button>
</div>
```

`.btn.active` fills with `--accent`. Toggle via JS.

### Cards

Bordered with `bg-card` background.

```html
<div class="card">
  <h3>Title</h3>
  <p style="color:var(--text-dim)">Description</p>
</div>

<div class="card-interactive">Clickable card (hover darkens border)</div>
```

Card group (continuous surface, internal separators):
```html
<div class="card-group">
  <div class="list-item-interactive">Item A</div>
  <div class="list-item-interactive">Item B</div>
  <div class="list-item-interactive">Item C</div>
</div>
```

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
  <textarea class="textarea" rows="3" placeholder="Write something..."></textarea>
</div>
```

Search box (icon + input, focus ring):
```html
<div class="search-box">
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
  <input type="text" placeholder="Search...">
</div>
```

### Tags

Rounded pill with border. Color variants use `color-mix` tinted backgrounds.

```html
<span class="tag">Default</span>
<span class="tag tag-accent">Active</span>
<span class="tag tag-success">Online</span>
<span class="tag tag-danger">Error</span>
<span class="tag tag-warning">Pending</span>
<span class="tag tag-mauve">Managed</span>
<span class="tag tag-sapphire">Info</span>
<span class="tag tag-outline">Outline</span>
```

### Checkbox

Square 16px checkbox with rounded corners. Toggle `.checked` via JS.

```html
<!-- Unchecked -->
<div class="check" onclick="this.classList.toggle('checked')"></div>

<!-- Checked -->
<div class="check checked"></div>

<!-- In a list item -->
<div class="list-item-interactive">
  <div class="check"></div>
  <span style="flex:1">Task text</span>
</div>
```

### Toggle switch

```html
<label class="toggle">
  <input type="checkbox">
  <span class="toggle-track"></span>
  Enable feature
</label>

<!-- Checked -->
<label class="toggle">
  <input type="checkbox" checked>
  <span class="toggle-track"></span>
  Active
</label>
```

### Filter tabs (segmented control)

Pill-shaped container with elevated active tab.

```html
<div class="filter-tabs">
  <button class="tab active">All</button>
  <button class="tab">Active</button>
  <button class="tab">Archived</button>
</div>
```

### Tab bar (underline navigation)

Underline-style for switching between views/pages.

```html
<div class="tab-bar">
  <button class="tab-item active">Dashboard</button>
  <button class="tab-item">Settings</button>
  <button class="tab-item">History</button>
</div>
```

**When to use which:**
- **Tab bar** (underline): switching between views/pages
- **Filter tabs** (segmented): filtering within the same view

### Progress bar

```html
<div class="progress-bar">
  <div class="progress-track">
    <div class="progress-fill" style="width: 72%"></div>
  </div>
  <span class="progress-label">72%</span>
</div>
```

Override fill color: `style="width:92%;background:var(--red)"`.

### Alerts

```html
<div class="alert alert-success">Operation completed.</div>
<div class="alert alert-danger">Something went wrong.</div>
<div class="alert alert-warning">Check your input.</div>
<div class="alert alert-info">Tip: you can also...</div>
```

### Data table

```html
<table class="data-table">
  <thead>
    <tr><th>Name</th><th>Status</th><th>Amount</th></tr>
  </thead>
  <tbody>
    <tr><td>Item A</td><td><span class="tag tag-success">Active</span></td><td>1,234</td></tr>
    <tr><td>Item B</td><td><span class="tag tag-danger">Error</span></td><td>567</td></tr>
  </tbody>
</table>
```

Row hover highlights with `bg-input`.

### Lists

```html
<!-- Static -->
<div class="list-item">Static item</div>

<!-- Clickable (hover bg) -->
<div class="list-item-interactive" onclick="...">Clickable item</div>

<!-- Inside card-group for auto-separators -->
<div class="card-group">
  <div class="list-item-interactive">
    <span class="dot dot-success"></span>
    <span style="flex:1">vault-server</span>
    <span class="tag tag-success">Running</span>
  </div>
  <div class="list-item-interactive">
    <span class="dot dot-danger"></span>
    <span style="flex:1">sandbox-worker</span>
    <span class="tag tag-danger">Stopped</span>
  </div>
</div>
```

### Dropdown menu

```html
<div class="dropdown-menu">
  <button class="dropdown-item">Edit</button>
  <button class="dropdown-item">Duplicate</button>
  <div class="dropdown-separator"></div>
  <button class="dropdown-item" style="color:var(--red)">Delete</button>
</div>
```

Position with CSS/JS. `dropdown-separator` adds a 1px divider.

### Accordion

Native `<details>/<summary>`, no JS needed. Chevron rotates on open.

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
  <button class="page-btn" disabled>&lt;</button>
  <button class="page-btn active">1</button>
  <button class="page-btn">2</button>
  <button class="page-btn">3</button>
  <button class="page-btn">&gt;</button>
</div>
```

### Slider

```html
<input type="range" class="slider" min="0" max="100" value="72">
```

Custom thumb with accent border, hover glow.

### Key-Value rows

Horizontal label-value pairs with auto-separators between rows.

```html
<div class="card">
  <div class="kv-row">
    <span class="kv-label">Status</span>
    <span class="kv-value"><span class="tag tag-success">Active</span></span>
  </div>
  <div class="kv-row">
    <span class="kv-label">Version</span>
    <span class="kv-value">1.2.0</span>
  </div>
  <div class="kv-row">
    <span class="kv-label">Created</span>
    <span class="kv-value">Apr 4, 2026</span>
  </div>
</div>
```

### Bar chart (vertical)

```html
<div class="bar-chart" style="height:120px">
  <div class="bar-chart-col">
    <div class="bar-chart-bar" style="height:40%"></div>
    <span class="bar-chart-label">Mon</span>
  </div>
  <div class="bar-chart-col">
    <div class="bar-chart-bar" style="height:80%"></div>
    <span class="bar-chart-label">Tue</span>
  </div>
  <div class="bar-chart-col">
    <div class="bar-chart-bar" style="height:60%; background:var(--green)"></div>
    <span class="bar-chart-label">Wed</span>
  </div>
</div>
```

### Horizontal bar chart

```html
<div class="hbar-row">
  <span class="hbar-label">Design</span>
  <div class="hbar-track"><div class="hbar-fill" style="width:85%"></div></div>
  <span class="hbar-value">85%</span>
</div>
<div class="hbar-row">
  <span class="hbar-label">Dev</span>
  <div class="hbar-track"><div class="hbar-fill" style="width:62%; background:var(--sapphire)"></div></div>
  <span class="hbar-value">62%</span>
</div>
```

### Ring chart

SVG donut. `stroke-dashoffset = 213.6 * (1 - percentage/100)`.

```html
<div class="ring-chart">
  <svg viewBox="0 0 80 80">
    <circle class="ring-bg" cx="40" cy="40" r="34"/>
    <circle class="ring-fill" cx="40" cy="40" r="34"
      stroke-dasharray="213.6" stroke-dashoffset="55.5"/>
  </svg>
  <span class="ring-chart-value">74%</span>
</div>
```

Override color: `style="stroke:var(--green)"` on `.ring-fill`.

### Dropzone

```html
<div class="dropzone">
  <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>
  <div style="font-size:var(--font-sm)">Drag & drop files here, or <span style="color:var(--accent)">browse</span></div>
  <div style="font-size:var(--font-xs);color:var(--text-dim)">PNG, JPG, PDF up to 10MB</div>
</div>
```

Hover activates accent border + tinted bg.

### Tooltip

CSS-only via `data-tooltip` attribute. Appears above on hover.

```html
<button class="btn" data-tooltip="Save changes">Save</button>
<button class="btn-icon" data-tooltip="Settings"><!-- icon --></button>
```

### Stat grid

4-column fused grid with 1px gap borders. Each item has a colored accent bar.

```html
<div class="stat-grid">
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--sapphire)"></div>
    <div class="stat-value">15,040</div>
    <div class="stat-label">Revenue</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--green)"></div>
    <div class="stat-value">1,480</div>
    <div class="stat-label">Users</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--yellow)"></div>
    <div class="stat-value">92%</div>
    <div class="stat-label">Uptime</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--red)"></div>
    <div class="stat-value">3</div>
    <div class="stat-label">Errors</div>
  </div>
</div>
```

### Chips

Removable tag with close button. Use for active filters, tag inputs.

```html
<div class="chip">
  Finance
  <button class="chip-close">
    <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
  </button>
</div>
```

### Settings rows

Label + description on the left, control on the right. Auto-separators between rows.

```html
<div class="card">
  <div class="settings-row">
    <div>
      <div class="settings-row-label">Notifications</div>
      <div class="settings-row-desc">Receive alerts when tasks complete</div>
    </div>
    <label class="toggle"><input type="checkbox" checked><span class="toggle-track"></span></label>
  </div>
  <div class="settings-row">
    <div>
      <div class="settings-row-label">Language</div>
      <div class="settings-row-desc">Display language</div>
    </div>
    <select class="select" style="width:140px"><option>English</option></select>
  </div>
</div>
```

### Avatars

32px circle with initials or image.

```html
<div class="avatar">AL</div>
<div class="avatar"><img src="/photo.jpg" alt="" style="width:100%;height:100%;border-radius:50%;object-fit:cover"></div>
```

### Misc utilities

**Keyboard shortcut:**
```html
Press <span class="kbd">⌘K</span> to search
```

**Skeleton loading:**
```html
<div class="skeleton" style="width:60%;height:16px"></div>
<div class="skeleton" style="width:100%;height:120px"></div>
```

**Meta (icon + text):**
```html
<span class="meta"><!-- 12px icon --> v1.2.0</span>
```

**Done state (strikethrough):**
```html
<span class="done">Completed task</span>
```

**Spacer (flex:1 push):**
```html
<div style="display:flex;align-items:center">
  <h3>Title</h3>
  <div class="spacer"></div>
  <button class="btn btn-sm">Action</button>
</div>
```

**Divider:**
```html
<hr class="divider">
```

**Empty state:**
```html
<div class="empty-state">
  <!-- 48px icon -->
  <p>No items yet.</p>
  <button class="btn btn-primary">Create</button>
</div>
```

**Footer stats:**
```html
<div class="footer-stats">3 / 10 items</div>
```

**Section label:**
```html
<div class="section-label">In progress</div>
```

**Danger zone:**
```html
<div class="danger-zone">
  <h3>Delete account</h3>
  <p>This action is irreversible.</p>
  <button class="btn btn-danger">Delete</button>
</div>
```

**Status dots:**
```html
<span class="dot dot-success"></span> Online
<span class="dot dot-danger"></span> Offline
<span class="dot dot-warning"></span> Pending
```

**Count badge:**
```html
<span class="count-badge">5</span>
```

Accent-filled pill. Use next to labels, tabs, nav items.

**Carousel dots:**
```html
<div class="carousel-dots">
  <button class="carousel-dot active"></button>
  <button class="carousel-dot"></button>
  <button class="carousel-dot"></button>
</div>
```

Active dot stretches to pill shape.

**Change indicators:**
```html
<span class="change-up">+5.2%</span>
<span class="change-down">-1.8%</span>
```

Arrow prefix auto-inserted via CSS `::before`.
