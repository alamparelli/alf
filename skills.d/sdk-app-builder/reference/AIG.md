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

**CRITICAL**: Every app MUST include this exact `<head>` block and `AlfSDK.init()` to stay in sync with the CC theme. Apps that skip this will look broken when the user switches themes.

### Required `<head>` (3 files)

```html
<link rel="stylesheet" id="alf-theme" href="/static/theme-sage.css">
<script src="/static/theme-init.js"></script>
<script src="/static/alf-app-sdk.js"></script>
<!-- alf-ui.css is auto-injected — do NOT import it manually -->
```

- **theme-*.css** — defines all `--bg`, `--text`, `--accent` etc. variables for that palette
- **theme-init.js** — reads `localStorage('alf-palette')` and swaps the CSS link before first paint
- **alf-app-sdk.js** — SDK for theme sync, storage, toasts, sheets, etc.

### Required `AlfSDK.init()` with theme handler

```js
AlfSDK.init({
  slug: 'my-app',
  onThemeChange: function(palette) {
    document.getElementById('alf-theme').href = '/static/theme-' + palette + '.css';
  }
});
```

The `onThemeChange` callback fires when the user switches themes in CC Settings. **Without it, the app freezes on its initial palette** — this is the #1 cause of theme inconsistency.

### Required body structure

Use one of these layout classes — **never write manual body CSS**:

| Layout | Class | Use for |
|--------|-------|---------|
| Simple page | `<body class="page">` | Basic apps, single-view |
| App with header | `<div class="app-shell">` | Apps with sticky header + actions |
| Sidebar + main | `<div class="workspace">` | Complex multi-panel apps |

### Available palettes (8)

`sage` (default), `studio`, `catppuccin`, `dracula`, `solarized`, `tokyo-night`, `github`, `nord`

Each palette defines light + dark variants via `prefers-color-scheme: dark` media query. The app doesn't need to handle dark mode — the CSS variables adapt automatically.

### DO NOT

- Do NOT add `<link>` to alf-ui.css — it's auto-injected
- Do NOT write `body { background: var(--bg); color: var(--text); font-family: ... }` — use `.page` or `.app-shell`
- Do NOT add `<style>` blocks for things covered by alf-ui.css classes
- Do NOT forget `id="alf-theme"` on the theme link — `onThemeChange` needs it
- Do NOT use `<link id="alf-theme-link">` — the correct id is `alf-theme`

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

All components below are available via `alf-ui.css` classes. No JS required.
**Design philosophy**: borderless surfaces — background contrast, not borders.

> **Full HTML templates**: See `reference/AIG-COMPONENTS.md` for copy-paste markup of every component.
> **Visual gallery**: See `reference/components.html` for a rendered preview.

Available components: `.btn`, `.btn-primary`, `.btn-danger`, `.btn-success`, `.btn-ghost`, `.btn-sm`, `.btn-lg`, `.btn-block`, `.btn-icon`, `.btn-icon-accent`, `.btn-group`, `.card`, `.card-header`, `.card-interactive`, `.card-group`, `.content-card`, `.input-row`, `.check`, `.form-group`, `.form-label`, `.form-hint`, `.form-row`, `.input`, `.select`, `.textarea`, `.toggle`, `.toolbar`, `.search-box`, `.tab-bar`, `.tab-item`, `.filter-tabs`, `.data-table`, `.stat-grid`, `.stat-item`, `.list`, `.list-item`, `.list-item-interactive`, `.tag`, `.tag-accent`, `.tag-success`, `.tag-danger`, `.tag-warning`, `.tag-mauve`, `.tag-sapphire`, `.meta`, `.alert`, `.alert-success`, `.alert-danger`, `.alert-warning`, `.alert-info`, `.empty-state`, `.loading-state`, `.spinner`, `.divider`, `.divider-sm`, `.section-label`, `.count`, `.progress-bar`, `.progress-track`, `.progress-fill`, `.progress-label`, `.dot`, `.dot-success`, `.dot-danger`, `.dot-warning`, `.footer-stats`, `.danger-zone`, `.kbd`, `.skeleton`, `.skeleton-text`, `.skeleton-circle`, `.backdrop`, `.done`, `.spacer`, `.avatar`, `.avatar-sm`, `.avatar-lg`, `.avatar-stack`, `.kv-row`, `.kv-label`, `.kv-value`, `.line-clamp-2`, `.line-clamp-3`, `.change-up`, `.change-down`, `.change-flat`, `.chip`, `.chip-close`, `.sticky`, `.accordion`, `.accordion-content`, `.prose`, `[data-tooltip]`, `.dropdown`, `.dropdown-menu`, `.dropdown-item`, `.breadcrumb`, `.pagination`, `.page-btn`, `.slider`, `.bar-chart`, `.hbar-chart`, `.sparkline`, `.ring-chart`, `.carousel`, `.carousel-cards`, `.carousel-peek`, `.carousel-full`, `.carousel-dots`, `.dropzone`, `.dropzone-compact`, `.file-item`, `.popover`

---

## Layout patterns

Three layout shells — pick one per app, never write manual body CSS.

> **Full HTML templates**: See `reference/AIG-COMPONENTS.md` for copy-paste markup of every layout.

| Layout | Class / Element | Use for |
|--------|----------------|---------|
| **Standard app page** | `<body class="page">` | Simple single-view apps. Sets padding, max-width, font, bg, color. |
| **List view with toolbar** | `.page` + `.toolbar` + `.card-group` + `.footer-stats` | Filterable list apps. |
| **Grid layout** | `.grid-2`, `.grid-3` | Card grids. Collapses to 1 col under 480px. |
| **App shell** | `.app-shell` > `.app-header` + `.app-body` + `.app-footer` | Sticky header + scrollable body + footer. Use `.app-body-wide` for dashboards. |
| **Workspace** | `.workspace` > `.workspace-sidebar` + `.workspace-main` | Sidebar + main panel (mail, notes, IDE). Add `.workspace-detail` for 3-col. Auto-hides sidebar on mobile. |
| **Settings page** | `.app-shell` + `.settings-section` + `.settings-row` | Toggle/select settings with label + description. |

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
  '</div>' +
  '<button data-action="cancel" class="btn">Cancel</button> ' +
  '<button data-action="save" class="btn btn-primary">Save</button>',
  {
    cancel: function() { AlfSDK.closeSheet(); },
    save: function(p) {
      save(p.field);
      AlfSDK.closeSheet();
    }
  }
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

### Page setup

- DO: `<body class="page">` — one class, zero custom body CSS
- DON'T: manual `body { padding: 2.5rem; max-width: 760px; margin: 0 auto; font-family: ...; background: ...; color: ...; }`

### Completed / done state

- DO: `<span class="done">Finished task</span>`
- DON'T: `<span style="text-decoration:line-through;color:gray">Finished task</span>`

### Spacers

- DO: `<div class="spacer"></div>` in flex containers
- DON'T: `<div style="flex:1"></div>`

### Key-value pairs

- DO: `<div class="kv-row"><span class="kv-label">Status</span><span class="kv-value">Active</span></div>`
- DON'T: `<div style="display:flex;justify-content:space-between"><span>Status</span><span>Active</span></div>`

### Avatars

- DO: `<div class="avatar">AL</div>`
- DON'T: `<div style="width:32px;height:32px;border-radius:50%;background:green;...">AL</div>`

### Percentage changes

- DO: `<span class="change-up">+5.2%</span>`
- DON'T: `<span style="color:green">▲ +5.2%</span>`

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

- [ ] **Visual reference**: copied HTML patterns from `reference/components.html` — no invented CSS
- [ ] Theme setup: `theme-*.css` + `theme-init.js` + `alf-app-sdk.js` in `<head>`
- [ ] `AlfSDK.init()` with `onThemeChange` handler
- [ ] Layout: `.page`, `.app-shell`, or `.workspace` — **no manual body CSS**
- [ ] Settings pages use `.settings-section` + `.settings-row` pattern
- [ ] All colors use CSS variables, zero hardcoded hex values
- [ ] Translucent backgrounds use `color-mix()`, not `rgba()`
- [ ] All spacing uses `--space-*` tokens or `alf-ui.css` classes
- [ ] Buttons use `.btn` + variant classes, **no borders**
- [ ] Forms use `.form-group` + `.form-label` + `.input`
- [ ] Input + button combos use `.input-row` (flex + gap)
- [ ] Cards use `.card` (borderless) or `.card-group` for lists
- [ ] Tags use `.tag` + `.tag-*` (translucent tinted — **never** `background: var(--accent)` solid fill)
- [ ] Lists use `.card-group` > `.list-item-interactive` pattern
- [ ] Collapsible sections use `.accordion` with `details/summary`
- [ ] Markdown / rich text uses `.prose` — never re-implement heading or paragraph styles manually
- [ ] Markdown inside accordion uses `.accordion-content.prose` (both classes together)
- [ ] Tooltips use `data-tooltip` attribute — no custom tooltip CSS
- [ ] Context menus use `.dropdown` + `.dropdown-menu`
- [ ] File uploads use `.dropzone` + `.file-item`
- [ ] Charts use `.bar-chart`, `.hbar-chart`, `.ring-chart`, or `.sparkline`
- [ ] Pagination uses `.pagination` + `.page-btn`
- [ ] Range inputs use `.slider` class
- [ ] Scrollable content uses `.carousel` + `.carousel-cards`/`.carousel-peek`/`.carousel-full`
- [ ] Checkboxes use `.check` / `.check.checked` — **no emoji**
- [ ] Completed items use `.done` class
- [ ] Flex spacers use `.spacer` — not `style="flex:1"`
- [ ] Icons are inline Lucide SVGs with `currentColor`, sized 12/14/16/20px
- [ ] Empty states use `.empty-state`
- [ ] Loading states use `.loading-state` + `.spinner`
- [ ] Async content uses `.skeleton-text` / `.skeleton` placeholders
- [ ] Destructive sections use `.danger-zone`
- [ ] Footer counts use `.footer-stats`
- [ ] Hover transitions are `0.15s`, not arbitrary values
- [ ] Touch targets are 44px+ on mobile
- [ ] No `border: 1px solid` on cards, buttons, tags, or alerts
- [ ] No inline styles for things covered by `alf-ui.css` classes
