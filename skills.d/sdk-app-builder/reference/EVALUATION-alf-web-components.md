# Evaluation: ALF Web Components (`<alf-*>`)

## Problem Statement

When 10 different LLM instances receive the same prompt ("create a todo app"), they produce
10 different implementations despite AIG compliance. The root cause: **CSS class composition
is non-deterministic** — the LLM must decide HTML structure, event wiring, class ordering,
and JS behavior for every component.

### Current variance hotspots in `components.html`

| Component | Lines of HTML+JS the LLM must invent | Variance risk |
|-----------|--------------------------------------|---------------|
| Filter tabs + active state toggle | ~8 lines + onclick JS | **HIGH** — 5+ ways to wire active state |
| List item with checkbox | ~5 lines + toggle JS | **HIGH** — check state, onclick, class toggling |
| Modal / Sheet form | ~20 lines + AlfSDK.sheet() JS | **CRITICAL** — form layout, open/close, validation |
| Data table with sorting | ~15 lines + sort JS | **HIGH** — thead/tbody structure varies |
| Search + filter combo | ~12 lines | **MEDIUM** — layout composition varies |
| Stat grid | ~6 lines | **LOW** — mostly declarative already |
| Dropdown menu + trigger | ~10 lines + show/hide JS | **HIGH** — positioning, backdrop, toggle logic |
| Settings rows with toggles | ~8 lines | **MEDIUM** — structure is semi-fixed |
| Accordion | ~6 lines (details/summary) | **LOW** — native HTML handles behavior |
| Pagination | ~8 lines + page change JS | **HIGH** — render logic, active state |

---

## Proposed Solution: `<alf-*>` Custom Elements

Lightweight custom elements (NO Shadow DOM, NO Lit, NO build step) that:
- Render directly into the light DOM using `alf-ui.css` classes
- Expose declarative attributes for all configuration
- Handle interactive behavior internally (active states, toggles, show/hide)
- Work inside sandboxed iframes with zero additional dependencies

### Design Principles

1. **Light DOM only** — components render as regular HTML, styled by `alf-ui.css`
2. **Zero dependencies** — vanilla `customElements.define()`, no Lit, no framework
3. **Attribute-driven** — all config via HTML attributes, no constructor JS needed
4. **Progressive** — components enhance existing CSS classes, not replace them
5. **Single file** — all components ship in one `alf-components.js` (~15-20 KB)

---

## Component Mapping: CSS Classes → Web Components

### Tier 1 — Critical (highest variance, ship first)

#### `<alf-tabs>`
```html
<!-- BEFORE: ~8 lines + onclick JS, high variance -->
<div class="filter-tabs">
  <button class="tab active" onclick="...">All</button>
  <button class="tab" onclick="...">Active</button>
  <button class="tab" onclick="...">Done</button>
</div>

<!-- AFTER: 1 element, zero JS needed -->
<alf-tabs value="all" variant="filter">
  <alf-tab value="all">All</alf-tab>
  <alf-tab value="active">Active</alf-tab>
  <alf-tab value="done">Done</alf-tab>
</alf-tabs>

<!-- Also supports underline variant -->
<alf-tabs value="overview" variant="underline">
  <alf-tab value="overview">Overview</alf-tab>
  <alf-tab value="settings">Settings</alf-tab>
</alf-tabs>
```
**Determinism gain**: Eliminates onclick wiring, active class management, tab-bar vs filter-tabs decision.
**Events**: `alf-tab-change` with `event.detail.value`

---

#### `<alf-dialog>` (replaces AlfSDK.sheet() for forms)
```html
<!-- BEFORE: ~20 lines of JS, CRITICAL variance -->
<script>
function openAdd() {
  AlfSDK.sheet({
    title: 'Add Item',
    content: '<div class="form-group"><label class="form-label">Title</label>' +
             '<input class="input" id="sheet-title" placeholder="..."></div>' +
             '<div class="modal-actions"><button class="btn" onclick="AlfSDK.closeSheet()">Cancel</button>' +
             '<button class="btn btn-primary" onclick="save()">Save</button></div>'
  });
}
</script>

<!-- AFTER: declarative, zero JS for structure -->
<alf-dialog id="add-dialog" label="Add Item">
  <alf-input label="Title" name="title" placeholder="Enter title..." required></alf-input>
  <alf-input label="Description" name="desc" type="textarea" placeholder="Optional..."></alf-input>
  <alf-select label="Priority" name="priority">
    <option value="low">Low</option>
    <option value="medium" selected>Medium</option>
    <option value="high">High</option>
  </alf-select>
</alf-dialog>
<script>
  // Only the logic, not the structure
  document.getElementById('add-dialog').addEventListener('alf-submit', function(e) {
    saveItem(e.detail); // { title: '...', desc: '...', priority: 'medium' }
  });
</script>
```
**Determinism gain**: Eliminates HTML string concatenation, form layout decisions, button placement, close logic.
**Methods**: `.open()`, `.close()` — **Events**: `alf-submit`, `alf-cancel`

---

#### `<alf-input>` / `<alf-select>` / `<alf-textarea>`
```html
<!-- BEFORE: 4-5 lines, medium variance -->
<div class="form-group">
  <label class="form-label">Email</label>
  <input class="input" placeholder="name@example.com">
  <div class="form-hint">We'll never share your email.</div>
</div>

<!-- AFTER: 1 element -->
<alf-input label="Email" placeholder="name@example.com" hint="We'll never share your email."></alf-input>
<alf-input label="Password" type="password" required></alf-input>
<alf-input label="Notes" type="textarea" placeholder="Write something..."></alf-input>
<alf-select label="Category" placeholder="Select...">
  <option value="work">Work</option>
  <option value="personal">Personal</option>
</alf-select>
```
**Determinism gain**: Eliminates form-group/form-label/form-hint structure decisions, label placement.

---

#### `<alf-list>` + `<alf-list-item>`
```html
<!-- BEFORE: ~5 lines per item, HIGH variance -->
<div class="card-group">
  <div class="list-item-interactive">
    <div class="check checked" onclick="this.classList.toggle('checked')"></div>
    <span style="flex:1">Buy groceries</span>
    <span class="tag tag-success">low</span>
  </div>
</div>

<!-- AFTER: declarative -->
<alf-list>
  <alf-list-item checkbox checked>
    Buy groceries
    <alf-tag slot="end" variant="success">low</alf-tag>
  </alf-list-item>
  <alf-list-item checkbox>
    Deploy v2.1
    <alf-tag slot="end" variant="danger">high</alf-tag>
  </alf-list-item>
</alf-list>
```
**Determinism gain**: Eliminates check div vs input checkbox decision, onclick wiring, flex-1 styling.
**Events**: `alf-check-change` with `event.detail.checked`

---

#### `<alf-dropdown>`
```html
<!-- BEFORE: ~10 lines + show/hide/position JS, HIGH variance -->
<div style="position:relative">
  <button class="btn-icon" onclick="toggleMenu(this)">...</button>
  <div class="dropdown-menu" id="menu1" style="display:none">
    <button class="dropdown-item" onclick="edit()">Edit</button>
    <button class="dropdown-item" onclick="dup()">Duplicate</button>
    <div class="dropdown-separator"></div>
    <button class="dropdown-item" style="color:var(--red)" onclick="del()">Delete</button>
  </div>
</div>

<!-- AFTER: zero positioning/toggle JS -->
<alf-dropdown>
  <button class="btn-icon" slot="trigger">...</button>
  <alf-menu-item value="edit">Edit</alf-menu-item>
  <alf-menu-item value="dup">Duplicate</alf-menu-item>
  <alf-menu-divider></alf-menu-divider>
  <alf-menu-item value="delete" danger>Delete</alf-menu-item>
</alf-dropdown>
```
**Determinism gain**: Eliminates positioning logic, backdrop handling, show/hide toggle.
**Events**: `alf-select` with `event.detail.value`

---

### Tier 2 — High value (moderate variance, ship second)

#### `<alf-stat-grid>`
```html
<!-- BEFORE: ~6 lines per stat -->
<div class="stat-grid">
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--accent)"></div>
    <div class="stat-value">1,284</div>
    <div class="stat-label">Total tasks</div>
  </div>
</div>

<!-- AFTER -->
<alf-stat-grid>
  <alf-stat value="1,284" label="Total tasks" color="accent"></alf-stat>
  <alf-stat value="892" label="Completed" color="green"></alf-stat>
  <alf-stat value="12" label="Overdue" color="red"></alf-stat>
</alf-stat-grid>
```

#### `<alf-data-table>`
```html
<!-- BEFORE: manual thead/tbody + tag embedding -->
<table class="data-table">
  <thead><tr><th>Name</th><th>Status</th><th>Role</th></tr></thead>
  <tbody>
    <tr><td>Alessandro</td><td><span class="tag tag-success">active</span></td><td>Admin</td></tr>
  </tbody>
</table>

<!-- AFTER: data-driven -->
<alf-data-table id="users">
  <alf-column key="name" label="Name"></alf-column>
  <alf-column key="status" label="Status" type="tag"></alf-column>
  <alf-column key="role" label="Role"></alf-column>
</alf-data-table>
<script>
  document.getElementById('users').data = [
    { name: 'Alessandro', status: { text: 'active', variant: 'success' }, role: 'Admin' }
  ];
</script>
```

#### `<alf-search-box>`
```html
<!-- BEFORE: 4 lines + SVG icon -->
<div class="search-box">
  <svg width="14" height="14" viewBox="0 0 24 24" ...>...</svg>
  <input placeholder="Search tasks...">
</div>

<!-- AFTER -->
<alf-search-box placeholder="Search tasks..."></alf-search-box>
```
**Determinism gain**: Eliminates SVG icon embedding (a HUGE variance source).

#### `<alf-alert>`
```html
<!-- AFTER -->
<alf-alert variant="success">Task completed successfully.</alf-alert>
<alf-alert variant="danger" dismissible>Failed to save changes.</alf-alert>
```

#### `<alf-pagination>`
```html
<!-- AFTER -->
<alf-pagination total="42" page-size="10" current="2"></alf-pagination>
```
**Events**: `alf-page-change` with `event.detail.page`

#### `<alf-settings-row>`
```html
<!-- AFTER -->
<alf-settings-row label="Dark mode" description="Use dark theme across all apps">
  <alf-toggle name="dark-mode"></alf-toggle>
</alf-settings-row>
```

---

### Tier 3 — Nice to have (low variance or rare usage)

| Component | Notes |
|-----------|-------|
| `<alf-card>` | Low variance — `.card` class is simple enough |
| `<alf-breadcrumb>` | Low frequency, simple structure |
| `<alf-progress>` | `<alf-progress value="65" max="100" label="13/20">` |
| `<alf-avatar>` | `<alf-avatar initials="AL" color="accent">` |
| `<alf-tag>` | `<alf-tag variant="success">active</alf-tag>` — useful inside other components |
| `<alf-empty-state>` | `<alf-empty-state icon="inbox" message="No items yet">` |
| `<alf-kbd>` | Very low priority |
| `<alf-tooltip>` | Already works via `data-tooltip` attribute |

---

## Composite Patterns (Tier 1.5)

The biggest win for determinism — high-level patterns that eliminate entire pages of decisions:

#### `<alf-crud-list>` (the "killer" component)
```html
<!-- Replaces ~80 lines of HTML+JS that varies wildly across instances -->
<alf-crud-list
  storage-prefix="todos"
  add-label="Add Task"
  empty-message="No tasks yet"
  searchable
  filterable
>
  <alf-crud-field name="title" label="Title" required></alf-crud-field>
  <alf-crud-field name="priority" label="Priority" type="select"
    options='[{"value":"low","label":"Low"},{"value":"med","label":"Medium"},{"value":"high","label":"High"}]'>
  </alf-crud-field>
  <alf-crud-field name="done" label="Done" type="checkbox"></alf-crud-field>
</alf-crud-list>
```
This single component handles: storage CRUD, search filtering, add/edit dialog, empty state,
stat counts, check toggle, delete confirmation — all internally.

#### `<alf-app-shell>`
```html
<!-- Replaces the layout boilerplate that varies per instance -->
<alf-app-shell>
  <span slot="title">My App</span>
  <button slot="actions" class="btn btn-primary btn-sm" onclick="add()">Add</button>

  <!-- app content goes here -->
</alf-app-shell>
```

---

## Impact on `components.html`

The refactored `components.html` would become the **living reference** for both humans
AND Claude. Each section would show the `<alf-*>` component with its attributes, making
the gallery simultaneously:

1. **Visual documentation** (renders in browser)
2. **LLM training material** (single canonical way to use each component)
3. **Copy-paste source** (Claude copies elements directly, no invention needed)

### Estimated reduction in LLM decision surface

| Metric | Before (CSS) | After (Web Components) |
|--------|-------------|----------------------|
| Tokens per typical todo app | ~800-1200 | ~200-400 |
| Structural decisions per component | 3-8 | 0-1 |
| JS wiring lines per interactive feature | 5-20 | 0-3 |
| Variant outputs across 10 instances | 10 different | 1-2 variants max |

---

## Implementation Plan

### Phase 1: Core components (`alf-components.js`)
1. `<alf-tabs>` + `<alf-tab>`
2. `<alf-input>` / `<alf-select>` / `<alf-textarea>`
3. `<alf-dialog>`
4. `<alf-list>` + `<alf-list-item>`
5. `<alf-dropdown>` + `<alf-menu-item>`
6. `<alf-search-box>`
7. `<alf-tag>`

### Phase 2: Data & layout components
8. `<alf-stat-grid>` + `<alf-stat>`
9. `<alf-data-table>` + `<alf-column>`
10. `<alf-alert>`
11. `<alf-pagination>`
12. `<alf-settings-row>`
13. `<alf-app-shell>`

### Phase 3: Composite patterns
14. `<alf-crud-list>` + `<alf-crud-field>`
15. `<alf-empty-state>`
16. `<alf-progress>`

### Phase 4: Update `components.html`
- Refactor the entire gallery to use `<alf-*>` components
- Keep backward compatibility (CSS classes still work)
- Update SKELETON.html to use components
- Update skill documentation

### Delivery
- Single file: `/static/alf-components.js` (~15-20 KB, vanilla JS)
- Auto-loaded alongside `alf-ui.css` in sandboxed iframes
- Zero breaking changes — existing CSS-only apps continue to work

---

## Shoelace/Web Awesome Comparison

| Aspect | Shoelace/Web Awesome | Proposed `<alf-*>` |
|--------|---------------------|-------------------|
| Shadow DOM | Yes (encapsulated) | No (light DOM, uses alf-ui.css) |
| Dependencies | Lit (~17 KB) | None |
| Total weight | ~200+ KB | ~15-20 KB |
| Theming | Own token system (`--sl-*`) | Uses existing AIG tokens (`--bg`, `--accent`) |
| Components | 50+ general purpose | 15-20 ALF-specific |
| CSP compatible | Needs config | Yes by design |
| Iframe injection | Complex | Drop-in `<script>` tag |
| CRUD patterns | None | Built-in (`<alf-crud-list>`) |
| AlfSDK integration | None | Native |

**Verdict**: Building custom `<alf-*>` components is the right call. Shoelace solves
the general web component problem; ALF needs components purpose-built for **LLM-driven
app generation with deterministic output**.

---

## Success Criteria

After implementation, when asking 10 Claude instances to "create a todo app with search,
filters, and add/edit":

- [ ] 10/10 produce structurally identical HTML (same elements, same attributes)
- [ ] 10/10 use `<alf-crud-list>` or equivalent composite pattern
- [ ] JS varies only in app-specific business logic, not in UI wiring
- [ ] Total token output per app reduced by 50%+
- [ ] Zero custom CSS in generated apps
