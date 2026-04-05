# ALF Component Reference — v3 (Web Components)

All interactive UI uses `<alf-*>` custom elements. They render as light-DOM HTML styled by `alf-ui.css`.
Auto-loaded via `<script type="module" src="/static/alf-components.js">` (injected automatically).

> **Rule**: Always use `<alf-*>` components. Never manually compose CSS classes for these patterns.
> CSS utility classes (`.flex`, `.gap-sm`, `.text-dim`, `.hidden`, etc.) are still used for layout and text styling.

---

## Tabs

```html
<alf-tabs value="all" variant="filter">
  <alf-tab value="all">All</alf-tab>
  <alf-tab value="active">Active</alf-tab>
  <alf-tab value="done">Done</alf-tab>
</alf-tabs>
```

| Attr | Values | Default |
|------|--------|---------|
| `value` | active tab value | — |
| `variant` | `filter` \| `underline` | `filter` |

Event: `alf-tab-change` → `e.detail.value`

---

## Forms

```html
<alf-input label="Name" name="name" placeholder="Enter name" hint="Publicly visible." required></alf-input>
<alf-input label="Notes" name="notes" type="textarea" placeholder="Write..."></alf-input>
<alf-select label="Category" name="cat" placeholder="Select...">
  <option value="work">Work</option>
  <option value="personal" selected>Personal</option>
</alf-select>
```

| Element | Attrs |
|---------|-------|
| `<alf-input>` | `label`, `name`, `type` (text\|password\|email\|textarea), `placeholder`, `hint`, `value`, `required`, `disabled` |
| `<alf-select>` | `label`, `name`, `placeholder`, `disabled` — contains `<option>` children |

JS: `element.value` (get/set)

---

## Search Box

```html
<alf-search-box placeholder="Search tasks..."></alf-search-box>
```

Icon built-in. Event: `alf-search` → `e.detail.value`. JS: `element.value`

---

## Dialog (Sheet Form)

```html
<alf-dialog id="add-dialog" label="Add Task">
  <alf-input label="Title" name="title" required></alf-input>
  <alf-select label="Priority" name="priority">
    <option value="low">Low</option>
    <option value="high">High</option>
  </alf-select>
</alf-dialog>
```

Methods: `.open()`, `.close()`. Events: `alf-submit` → `e.detail` (object with field values), `alf-cancel`.

Uses `AlfSDK.sheet()` internally — Cancel/Save buttons are auto-generated.

---

## List

```html
<alf-list>
  <alf-list-item checkbox checked>Buy groceries <alf-tag variant="success">low</alf-tag></alf-list-item>
  <alf-list-item checkbox>Deploy v2.1 <alf-tag variant="danger">high</alf-tag></alf-list-item>
  <alf-list-item>Static item (no checkbox)</alf-list-item>
</alf-list>
```

| Attr | Effect |
|------|--------|
| `checkbox` | Adds check toggle |
| `checked` | Pre-checked state |

Event: `alf-check-change` → `e.detail.checked`. JS: `element.checked` (get/set)

---

## Dropdown Menu

```html
<alf-dropdown>
  <button class="btn-icon" slot="trigger">⋯</button>
  <alf-menu-item value="edit">Edit</alf-menu-item>
  <alf-menu-item value="dup">Duplicate</alf-menu-item>
  <alf-menu-divider></alf-menu-divider>
  <alf-menu-item value="delete" danger>Delete</alf-menu-item>
</alf-dropdown>
```

Positioning and open/close automatic. Event: `alf-select` → `e.detail.value`

---

## Tag

```html
<alf-tag variant="success">active</alf-tag>
<alf-tag variant="danger" outline>error</alf-tag>
```

Variants: `accent`, `success`, `danger`, `warning`, `mauve`, `sapphire`. Add `outline` for outline style.

---

## Alert

```html
<alf-alert variant="success">Task completed.</alf-alert>
<alf-alert variant="danger" dismissible>Failed to save.</alf-alert>
```

Variants: `success`, `danger`, `warning`, `info`. Add `dismissible` for close button. Event: `alf-dismiss`.

---

## Toggle

```html
<alf-toggle name="dark-mode" checked></alf-toggle>
```

Event: `alf-change` → `e.detail.checked`, `e.detail.name`. JS: `element.checked` (get/set)

---

## Stat Grid

```html
<alf-stat-grid>
  <alf-stat value="1,284" label="Total" color="accent"></alf-stat>
  <alf-stat value="892" label="Done" color="green"></alf-stat>
  <alf-stat value="12" label="Overdue" color="red"></alf-stat>
</alf-stat-grid>
```

Update: `element.setAttribute('value', '42')`. Colors: any CSS var name (accent, green, red, yellow, sapphire, mauve).

---

## Data Table

```html
<alf-data-table id="tbl">
  <alf-column key="name" label="Name"></alf-column>
  <alf-column key="status" label="Status" type="tag"></alf-column>
  <alf-column key="amount" label="Amount"></alf-column>
</alf-data-table>
<script>
document.getElementById('tbl').data = [
  { name: 'Item A', status: { text: 'Active', variant: 'success' }, amount: '1,234' }
];
</script>
```

Column `type`: `text` (default), `tag` (expects `{ text, variant }`). Set `.data` to re-render.

---

## Pagination

```html
<alf-pagination total="42" page-size="10" current="2"></alf-pagination>
```

Event: `alf-page-change` → `e.detail.page`. Auto-renders page buttons.

---

## Progress

```html
<alf-progress value="13" max="20" label="13/20"></alf-progress>
```

Updates when attributes change.

---

## Settings Row

```html
<alf-settings-row label="Notifications" description="Get alerts when tasks complete">
  <alf-toggle name="notif" checked></alf-toggle>
</alf-settings-row>
```

Wrap in `.card` for grouped settings.

---

## Accordion

```html
<alf-accordion>
  <alf-accordion-item label="What is ALF?" open>Content here.</alf-accordion-item>
  <alf-accordion-item label="How do apps work?">More content.</alf-accordion-item>
</alf-accordion>
```

---

## Breadcrumb

```html
<alf-breadcrumb>
  <alf-crumb href="#">Home</alf-crumb>
  <alf-crumb href="#">Apps</alf-crumb>
  <alf-crumb>Current</alf-crumb>
</alf-breadcrumb>
```

Last crumb auto-styled as current. Separators auto-generated.

---

## Empty State

```html
<alf-empty-state message="No items yet" action="Add first item"></alf-empty-state>
```

Attrs: `message`, `action` (button text, optional), `icon="none"` (hide icon). Event: `alf-action`.

---

## App Shell

```html
<alf-app-shell>
  <span slot="title">My App</span>
  <button slot="actions" class="btn btn-primary btn-sm" onclick="add()">Add</button>
  <!-- app body content here -->
</alf-app-shell>
```

---

## Avatar

```html
<alf-avatar initials="AL" color="accent"></alf-avatar>
<alf-avatar initials="S" size="sm" color="mauve"></alf-avatar>
```

Sizes: `sm`, default, `lg`.

---

## Key-Value Row

```html
<alf-kv-row label="Status"><span class="dot dot-success"></span> Running</alf-kv-row>
<alf-kv-row label="Uptime">14d 6h 32m</alf-kv-row>
```

---

## Chip

```html
<alf-chip>React</alf-chip>
<alf-chip>Go</alf-chip>
```

Removable by default (close button). Event: `alf-remove` → `e.detail.value`.

---

## Input Row

```html
<alf-input-row placeholder="Add a new item..." button="Add" name="item"></alf-input-row>
```

Event: `alf-submit` → `e.detail.value`. Input clears on submit. Enter key works.

---

## Charts

### Bar Chart

```html
<alf-bar-chart id="chart" height="100px"></alf-bar-chart>
<script>
document.getElementById('chart').data = [
  { label: 'Mon', value: 40 },
  { label: 'Tue', value: 70, color: 'green' }
];
</script>
```

### Horizontal Bar

```html
<alf-hbar label="Chrome" value="65%" percent="65"></alf-hbar>
<alf-hbar label="Safari" value="20%" percent="20" color="sapphire"></alf-hbar>
```

### Sparkline

```html
<alf-sparkline id="spark"></alf-sparkline>
<script>document.getElementById('spark').data = [40, 65, 45, 80, 60, 90, 75];</script>
```

Attr: `color` (e.g. `red`).

### Ring Chart

```html
<alf-ring-chart value="70"></alf-ring-chart>
<alf-ring-chart value="30" color="green"></alf-ring-chart>
```

---

## Dropzone

```html
<alf-dropzone hint="PNG, JPG up to 10MB" multiple></alf-dropzone>
<alf-dropzone compact label="Attach a file..."></alf-dropzone>
```

Attrs: `label`, `hint`, `compact`, `multiple`, `accept`. Event: `alf-files` → `e.detail.files`.

---

## Carousel

```html
<alf-carousel variant="cards">
  <div class="card">Slide 1</div>
  <div class="card">Slide 2</div>
</alf-carousel>
```

Variants: `cards`, `peek`, `full`. Items auto-wrapped in `.carousel-item`.

---

## Button Group

```html
<alf-btn-group value="Day">
  <button class="btn" data-value="Day">Day</button>
  <button class="btn" data-value="Week">Week</button>
  <button class="btn" data-value="Month">Month</button>
</alf-btn-group>
```

Event: `alf-change` → `e.detail.value`. Active state managed automatically.

---

## Danger Zone

```html
<alf-danger-zone>
  <h3>Delete workspace</h3>
  <p>This cannot be undone.</p>
  <button class="btn btn-danger btn-sm">Delete</button>
</alf-danger-zone>
```

---

## Toolbar

```html
<alf-toolbar search="Search tasks...">
  <alf-tabs value="all" variant="filter">
    <alf-tab value="all">All</alf-tab>
    <alf-tab value="active">Active</alf-tab>
    <alf-tab value="done">Done</alf-tab>
  </alf-tabs>
</alf-toolbar>
```

Attr: `search` (placeholder — adds search box with flex:1). Event: `alf-search` → `e.detail.value`. JS: `element.searchValue`.

Children are placed after the search box (tabs, buttons, etc.).

---

## Drawer

```html
<alf-drawer id="detail" label="Item Details">
  <alf-kv-row label="Name">Project Alpha</alf-kv-row>
  <alf-kv-row label="Status"><span class="dot dot-success"></span> Active</alf-kv-row>
</alf-drawer>
<script>
document.getElementById('detail').open();
</script>
```

Methods: `.open()`, `.close()`. Events: `alf-open`, `alf-close`. Access body: `.body` (DOM element).

Slide-in panel from right with overlay. Close via overlay click or close button.

---

## Loading

```html
<alf-loading active variant="skeleton"></alf-loading>
<alf-loading active variant="spinner" message="Fetching data..."></alf-loading>
```

Attrs: `active` (show/hide), `variant` (`skeleton`|`spinner`), `message` (spinner only).

Methods: `.show()`, `.hide()`.

---

## Nav Row

```html
<alf-nav-row label="March 2026"></alf-nav-row>
```

Prev/next buttons with centered label. Update: `element.setAttribute('label', 'April 2026')`.

Event: `alf-nav` → `e.detail.direction` (`prev`|`next`).

---

## Components without `<alf-*>` equivalent

These use CSS classes directly (simple enough, no JS behavior):

| Pattern | Usage |
|---------|-------|
| Buttons | `<button class="btn btn-primary btn-sm">Save</button>` |
| Cards | `<div class="card">...</div>`, `.card-interactive`, `.card-group` |
| Tooltip | `<span data-tooltip="Help text">Hover</span>` |
| Status dots | `<span class="dot dot-success"></span>` |
| Misc text | `.meta`, `.done`, `.kbd`, `.section-label`, `.footer-stats`, `.divider` |
| Layout | `.flex`, `.flex-col`, `.grid-2`, `.grid-3`, `.hidden`, `.spacer` |
| Slider | `<input type="range" class="slider">` |
| Workspace | `.workspace` > `.workspace-sidebar` + `.workspace-main` — see `components/workspace.md` |
| Popover | `.popover` > `.popover-content` (focus-within) |
| Prose | `.prose` wrapper for rendered markdown |
