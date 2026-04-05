# AIG Component Reference — v2

Full HTML templates for all `alf-ui-v2.css` components. Each component has its own file — read only what you need.

> See `AIG.md` for design rules, tokens, and the Do/Don't checklist.

---

## Design philosophy

**Bordered surfaces**. Cards, buttons, inputs, and tags use `border: 1px solid var(--border)` with `box-shadow: var(--shadow-sm)`. Focus states use a ring: `box-shadow: var(--ring)`.

This replaces v1's borderless/contrast-only approach.

---

## Components

| Component | File | What |
|-----------|------|------|
| Buttons | `components/buttons.md` | btn, btn-primary, btn-icon, button groups, toggle |
| Cards | `components/cards.md` | card, card-interactive, card-group |
| Forms | `components/forms.md` | input, select, textarea, form-group, search-box |
| Tags | `components/tags.md` | tag, tag-accent, tag-success, tag-danger, tag-outline |
| Checkbox & Toggle | `components/checkbox-toggle.md` | check, checked, toggle switch |
| Tabs | `components/tabs.md` | filter-tabs (segmented), tab-bar (underline) |
| Progress | `components/progress.md` | progress-bar, progress-fill, progress-label |
| Alerts | `components/alerts.md` | alert-success, alert-danger, alert-warning, alert-info |
| Data Table | `components/data-table.md` | data-table with thead/tbody, row hover |
| Lists | `components/lists.md` | list-item, list-item-interactive, card-group lists |
| Dropdown | `components/dropdown.md` | dropdown-menu, dropdown-item, dropdown-separator |
| Accordion | `components/accordion.md` | details/summary, accordion-content |
| Breadcrumb | `components/breadcrumb.md` | breadcrumb nav with separators |
| Pagination | `components/pagination.md` | page-btn, active state |
| Slider | `components/slider.md` | range input with custom thumb |
| Key-Value | `components/key-value.md` | kv-row, kv-label, kv-value |
| Charts | `components/charts.md` | bar-chart, hbar, ring-chart (SVG donut) |
| Dropzone | `components/dropzone.md` | file drop area with hover state |
| Tooltip | `components/tooltip.md` | CSS-only via data-tooltip attribute |
| Stat Grid | `components/stat-grid.md` | stat-item, stat-bar, stat-value, stat-label |
| Chips | `components/chips.md` | removable tag with chip-close button |
| Settings Rows | `components/settings-rows.md` | settings-row with label + control |
| Avatars | `components/avatars.md` | avatar circle with initials or image |
| Loading & Utilities | `components/loading.md` | skeleton, kbd, meta, dot, spacer, divider, empty-state, danger-zone, count-badge, carousel-dots, change indicators |
| Workspace | `components/workspace.md` | workspace, workspace-sidebar, workspace-main, workspace-detail, workspace-back, sidebar-item, sidebar-nav, mobile toggle |
| Content Card | `components/content-card.md` | content-card with media + body |
| Input Row | `components/input-row.md` | input-row (input+button), form-row (side-by-side) |
| Carousel | `components/carousel.md` | carousel-cards, carousel-peek, carousel-full |
| Popover | `components/popover.md` | focus-within content panel |
| Prose | `components/prose.md` | rendered markdown/HTML wrapper |
| Sparkline | `components/sparkline.md` | inline mini bar chart |
| Grid | `components/grid.md` | grid-2, grid-3 responsive layouts |
| Nav Row | `components/nav-row.md` | prev/next navigation with centered label |
| Day Grid | `components/day-grid.md` | calendar day cells for trackers, heatmaps |
