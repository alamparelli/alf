# Frontend (AlfSDK) -- Reference

All apps with a web UI use **AlfSDK** for theme sync, toasts, and parent SPA communication.
Vanilla JS only -- no frameworks, no build step, CSP-safe.

> **Design system**: `alf-ui.css` is auto-injected into all app iframes. Use its classes (`.btn`, `.card`, `.input`, `.form-group`, etc.) instead of writing inline styles. See `AIG-COMPONENTS.md` for the full component reference.

---

## Starting template

> **Copy `reference/SKELETON.html`** as your starting point. It includes correct theme setup, stat-grid, card-group, filter-tabs, CRUD with AlfSDK.storage, and sheets — all using alf-ui.css classes.

---

## AlfSDK reference

```js
// Core
AlfSDK.VERSION                         // '4.0.0'
AlfSDK.init({ slug, onThemeChange,     // Init. Call once on load. REQUIRED.
  onVisible, onHidden, onDestroy })    //   Lifecycle callbacks (see Lifecycle section)
AlfSDK.tool(action, args)              // Run CLI tool. Returns Promise<string>.
AlfSDK.api(path, opts)                 // Authenticated fetch (Bearer token). Returns Promise<parsed JSON|text>. Throws on non-2xx.
AlfSDK.fetch(path, opts)               // Raw authenticated fetch. Returns Promise<Response> (not parsed). For binary/streaming.
AlfSDK.bash(cmd)                       // Execute shell command via /api/bash.
AlfSDK.action(target, action, params)  // Call cross-app action. Returns Promise<any>.
AlfSDK.navigate(view)                  // Navigate parent SPA ('chat', 'settings', 'vault').
AlfSDK.toast(msg, type)                // Toast in parent: 'success', 'error', 'info'.
AlfSDK.getTheme()                      // Returns { palette, dark } (from parent handshake).
AlfSDK.confirm(message, opts?)         // Confirmation dialog → Promise<boolean>
AlfSDK.prompt(message, opts?)          // Input dialog → Promise<string|null>
                                       //   opts: { title, placeholder, ok, cancel, multiline }

// Sheets (bottom-sheet modals rendered in parent)
AlfSDK.sheet(html, actions?)           // Show HTML in bottom-sheet modal
                                       //   actions: { 'name': callback(params) }
                                       //   Buttons: add data-action="name" to elements
                                       //   Forms: inputs with name="" auto-collected as params
AlfSDK.updateSheet(html)               // Update sheet content without closing
AlfSDK.closeSheet()                    // Close current sheet

// Storage (server-side, persists across updates)
AlfSDK.storage.get(key?)               // Get value or full store
AlfSDK.storage.set(key, value)         // Set value (or pass object for batch)
AlfSDK.storage.remove(key)             // Delete key
AlfSDK.storage.clear()                 // Clear all
AlfSDK.storage.keys()                  // List all keys → Promise<string[]>
AlfSDK.storage.entries()               // List all entries → Promise<{key,value}[]>

// Upload
AlfSDK.upload(file)                    // Upload File to data/uploads/ → Promise<{path,name,size}>

// Events (auto-namespaced by slug)
AlfSDK.events.on(event, handler)       // Listen (bare name = own app, 'slug:event' = cross-app)
AlfSDK.events.off(event, handler)      // Unsubscribe
AlfSDK.events.emit(event, data)        // Emit (auto-prefixed with slug)

// Clipboard (requires 'clipboard' permission for marketplace apps)
AlfSDK.clipboard.write(text)           // Copy to clipboard → Promise<void>
AlfSDK.clipboard.read()               // Read from clipboard → Promise<string>

// Badge (app icon badge count)
AlfSDK.badge.set(count)               // Set badge number
AlfSDK.badge.increment()              // Increment by 1
AlfSDK.badge.clear()                  // Clear badge

// Viewport
AlfSDK.viewport.isMobile()            // true if width <= 768px
AlfSDK.viewport.isPWA()               // true if standalone/fullscreen mode
AlfSDK.viewport.safeArea()            // { top, right, bottom, left } in px
AlfSDK.viewport.orientation()         // 'portrait' or 'landscape'
AlfSDK.viewport.size()                // { width, height }
AlfSDK.viewport.onChange(callback)    // Register resize/orientation listener

// Haptics (vibration API, no-op if unavailable)
AlfSDK.haptics.tap()                  // Light tap (10ms)
AlfSDK.haptics.notify()               // Double pulse [30,50,30]
AlfSDK.haptics.success()              // Rising [10,30,20,30,40]
AlfSDK.haptics.error()                // Heavy buzz [50,50,100]
AlfSDK.haptics.vibrate(pattern)       // Custom pattern array
AlfSDK.haptics.isAvailable()          // true if vibration supported

// Audio (shared AudioContext, mobile-safe unlock)
AlfSDK.audio.getContext()             // Returns AudioContext (creates if needed)
AlfSDK.audio.isUnlocked()            // true if AudioContext is running
AlfSDK.audio.onUnlock(callback)      // Called when AudioContext unlocks (gesture-triggered)
AlfSDK.audio.load(url)               // Fetch + decode → Promise<AudioBuffer> (cached)
AlfSDK.audio.play(buffer, opts?)     // Play buffer. opts: { volume: 0-1, loop: bool }
AlfSDK.audio.playUrl(url, opts?)     // Load + play in one call

// i18n
AlfSDK.i18n.locale()                 // Full locale string (e.g. 'en-US')
AlfSDK.i18n.lang()                   // Language code (e.g. 'en')
AlfSDK.i18n.dir()                    // 'ltr' or 'rtl'
AlfSDK.i18n.languages()              // Array of preferred languages

// Error reporting (automatic — captured on init, logged to /api/apps/{slug}/errors)
```

---

## CSS variables (theme)

Only use these -- never hardcode colors. Full reference in `AIG-COMPONENTS.md`.

| Variable | Usage |
|---|---|
| `--bg` | Page background |
| `--bg-card` | Card / surface background |
| `--bg-input` | Input field background |
| `--text` | Primary text |
| `--text-dim` | Secondary / placeholder text |
| `--border` | Borders, dividers |
| `--accent` | Primary action color |
| `--on-accent` | Text on accent background |
| `--radius` | Border radius (default 8px) |
| `--green` | Success / positive |
| `--red` | Error / negative / destructive |
| `--yellow` | Warning |
| `--mauve` | Purple accent |
| `--sapphire` | Blue accent |
| `--pink` | Pink accent |
| `--teal` | Teal accent |
| `--peach` | Orange accent |
| `--lavender` | Light purple accent |
| `--danger` | Alias for red |
| `--success` | Alias for green |
| `--error` | Alias for red |

---

## Design tokens

Layout-agnostic tokens for spacing, typography, and shadows. Use these instead of hardcoded values.

### Spacing

| Variable | Value |
|---|---|
| `--space-xs` | 4px |
| `--space-sm` | 8px |
| `--space-md` | 16px |
| `--space-lg` | 24px |
| `--space-xl` | 32px |

### Typography (font sizes)

| Variable | Value |
|---|---|
| `--font-xs` | 11px |
| `--font-sm` | 13px |
| `--font-md` | 15px |
| `--font-lg` | 18px |
| `--font-xl` | 24px |

### Shadows

| Variable | Value |
|---|---|
| `--shadow-sm` | `0 1px 2px rgba(0,0,0,0.08)` |
| `--shadow-md` | `0 4px 12px rgba(0,0,0,0.12)` |
| `--shadow-lg` | `0 8px 24px rgba(0,0,0,0.16)` |

---

## Sandbox

App iframes run with `sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox"`. This means:

- **No `localStorage` or `sessionStorage`** — use `AlfSDK.storage` instead
- **No `document.cookie`** — auth is handled automatically via Bearer token
- **No `credentials: 'same-origin'`** — use `AlfSDK.api()` which sets the Bearer header
- **No `parent.postMessage()`** — the SDK uses MessageChannel (automatic)
- **No `top.location` or `parent.document`** — sandboxed, no access to parent frame

**Do:** Use `AlfSDK.storage`, `AlfSDK.api()`, `AlfSDK.getTheme()`.
**Don't:** Use `localStorage`, `fetch()` with credentials, `document.cookie`.

---

## Rules

1. **Follow AIG** -- use `alf-ui.css` classes for buttons, cards, forms, lists. See `AIG-COMPONENTS.md`.
2. **Always init AlfSDK** at the top of the script block
3. **Always include `onThemeChange`** to sync theme from parent SPA
4. **Set `font-family` explicitly** -- `system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`. Google Fonts are blocked by CSP in iframes.
5. **Load required static files**: `theme-*.css`, `theme-init.js`, `alf-app-sdk.js`, `alf-components.js`
6. **No external scripts/stylesheets** -- CSP `style-src 'self'` blocks them. `alf-ui.css` is auto-injected. **Never use absolute URLs** (even to the CC domain like `https://cc.example.com/static/...`) — they fail in the iframe CSP. Use relative paths only: `/static/alf-ui.css`.
7. **Lightweight eval-based frameworks OK** -- Alpine.js, Petite Vue work (`unsafe-eval` is in CSP). No build-step frameworks (React, Vue SPA, Angular).
8. **Inline `<style>` only** -- no external CSS files you create. Minimize inline styles — prefer `alf-ui.css` classes.
9. **Lucide SVG icons** -- inline SVG from lucide.dev. No icon fonts. No emoji as icons (unless user asks).
10. **XSS protection** -- always escape user content with a `div.textContent` wrapper (the `esc()` helper above)
11. **`font-family: inherit`** is NOT sufficient -- always set explicitly (see rule 4)
12. **Use spacing tokens** -- `--space-xs` to `--space-xl` or classes `.gap-sm`, `.p-md`, `.mb-lg` etc.
13. **Never use `fetch()` directly** -- use `AlfSDK.api()` for JSON APIs (auto-parses, throws on non-2xx) or `AlfSDK.fetch()` for binary/streaming (returns raw `Response`). Both add Bearer auth automatically.

---

## Lifecycle hooks

```js
AlfSDK.init({
  slug: 'my-app',
  onThemeChange: function(palette, isDark) { /* theme switched — palette='sage'|'rose'|..., isDark=bool */ },
  onVisible: function() { /* tab/app became visible — resume polling, animations */ },
  onHidden: function() { /* tab/app hidden — pause work, save state */ },
  onDestroy: function() { /* app being torn down — cleanup */ }
});
```

- `onVisible`/`onHidden` fire on Page Visibility API changes and when the parent SPA switches tabs.
- `onDestroy` fires when the iframe is removed from the DOM.
- `api()`, `fetch()`, `tool()`, `bash()`, `storage.*` calls are queued until the parent sends the init-context (Bearer token + theme) via MessageChannel. Safe to call immediately after `init()` — they wait automatically.

---

## Responsive design

Use `AlfSDK.viewport.onChange()` to react to viewport changes — don't just check `isMobile()` once at load time.

```js
// Adapt layout when viewport changes
AlfSDK.viewport.onChange(function(info) {
  if (!info.mobile && !currentId && items.length > 0) {
    selectItem(items[0].id); // auto-select first on desktop
  }
});
```

Available in the callback: `info.mobile` (bool), `info.orientation` ('portrait'/'landscape'), `info.size` ({width, height}).

---

## Sheets (bottom-sheet modals)

Sheets render HTML in a parent-level modal (bottom-sheet on mobile, centered on desktop).
HTML is sanitized -- safe tags, attributes (including `style`), and `data-*` attributes are preserved. No scripts.

```js
// Simple informational sheet
AlfSDK.sheet('<h3>Details</h3><p>Some content here</p>');

// Sheet with action buttons (data-action + object map)
AlfSDK.sheet(
  '<h3>Confirm Delete</h3><p>This cannot be undone.</p>' +
  '<button data-action="cancel" class="btn">Cancel</button>' +
  '<button data-action="delete" class="btn btn-danger">Delete</button>',
  {
    cancel: function() { AlfSDK.closeSheet(); },
    delete: function() { doDelete(); AlfSDK.closeSheet(); }
  }
);

// Sheet with form inputs — values auto-collected by name attribute
// > **Prefer `<alf-dialog>`** for form sheets (see AIG-COMPONENTS.md). Use `AlfSDK.sheet()` only for custom non-form content.
AlfSDK.sheet(
  '<h3>Edit Item</h3>' +
  '<div class="form-group"><label class="form-label">Title</label><input class="input" name="title" value="Current Title"></div>' +
  '<div class="form-group"><label class="form-label">Notes</label><textarea class="input" name="notes" rows="3"></textarea></div>' +
  '<button data-action="save" class="btn btn-primary">Save</button>',
  { save: function(params) {
    // params = { title: '...', notes: '...' }
    saveItem(params);
    AlfSDK.closeSheet();
  }}
);

// Update sheet content dynamically
AlfSDK.updateSheet('<h3>Loading...</h3><p>Please wait</p>');
```

**Button click handling**: Elements with `data-action="name"` trigger the matching action callback.
Form inputs with `name` or `data-field` attributes are collected into the `params` object.
Checkboxes collect `"true"`/`"false"`. Radio buttons collect the `value` of the checked radio in the group.

**No inline JS**: `onclick`, `onchange`, and all event handler attributes are stripped by the sanitizer. Use `data-action` for buttons and native form elements (`<input type="radio">`, `<select>`, `<input type="checkbox">`) for user choices — their values are auto-collected when the action button is clicked.

```js
// Radio group — value auto-collected as params.mood
AlfSDK.sheet(
  '<h3>Pick Mood</h3>' +
  '<label><input type="radio" name="mood" value="happy"> Happy</label>' +
  '<label><input type="radio" name="mood" value="neutral" checked> Neutral</label>' +
  '<label><input type="radio" name="mood" value="sad"> Sad</label>' +
  '<button data-action="save">Save</button>',
  { save: function(params) { /* params.mood = "happy"|"neutral"|"sad" */ } }
);
```

**Sheet styling**: The sheet container has `background: var(--bg)` and inherits the theme variables. Sheet HTML gets base styles for headings, paragraphs, inputs, buttons, and labels automatically. Use `alf-ui.css` classes (`.btn`, `.input`, `.form-group`, etc.) inside sheets for consistency with the rest of the workspace.

**Dismiss animation**: On mobile, sheets slide down when dismissed (swipe down > 120px, tap backdrop, or `AlfSDK.closeSheet()`).

---

## Permissions

**All apps** (local and marketplace) must declare permissions in `manifest.json`:

```json
{ "permissions": ["storage", "bash", "clipboard"] }
```

| Permission | What it grants | Untrusted marketplace apps |
|---|---|---|
| `storage` | Server-side key/value storage | Allowed |
| `events` | Cross-app event emission | Allowed |
| `clipboard` | Read/write clipboard | Allowed |
| `bash` | Shell command execution | Denied |
| `upload` | File uploads | Denied |
| `network` | Network access in sandboxed bash | Denied |

- **Local apps** must declare permissions — undeclared permissions return `403`.
- **Untrusted marketplace apps** are capped to `storage`, `events`, `clipboard` regardless of what they declare.
- **Trusted marketplace apps** (verified in registry) can use all declared permissions.
- APIs that require a permission return `403` with `{"error": "permission denied: <perm>"}`.

### Sandbox execution

Both `AlfSDK.bash()` calls and app backend servers run inside a chroot jail with an allowlist filesystem. Apps see only:

- System binaries (read-only): `/bin`, `/usr`, `/lib`, `/sbin`, `/lib64`
- Minimal devices: `/dev/{null,zero,urandom,random}`
- Fresh `/proc` mount (PID namespace isolated), private `/tmp` (tmpfs)
- TLS CA certs, DNS (servers always; bash only with `network` permission)
- Own data: `/home/alf/data/apps/<slug>/data/` (bash) or full app dir (server)
- Shared tools: `/home/alf/data/tools/` (read-only)

Apps do NOT see: other apps' directories, `/opt/alf/vault-data/`, `/home/alf/.claude/`, `/home/alf/data/external/`, `/run/secrets/`, or VAULT_TOKEN.

### HTTP API isolation

Apps in iframes can only access:

- `/apps/{own-slug}/api/*` — own REST proxy
- `/api/apps/{own-slug}/*` — own storage/upload/errors
- `/api/bash` — sandboxed bash (permission checked)
- `/api/events` — SSE events (read-only)

All other `/api/*` endpoints return 403 from app context.

### Static file allowlist

Only web-safe extensions are served via `/apps/{slug}/`: `.html`, `.css`, `.js`, `.mjs`, `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.ico`, `.webp`, `.avif`, `.woff`, `.woff2`, `.ttf`, `.otf`, `.eot`, `.mp3`, `.ogg`, `.wav`, `.mp4`, `.webm`, `.json`, `.xml`, `.txt`, `.csv`, `.map`, `.wasm`, `.pck`. Source code (`.go`, `.py`), databases (`.db`, `.sqlite`), and internal files return 404.

---

## REST server apps (API proxy)

Apps with a backend server (`service.json`) get automatic API proxying.
No `AlfSDK.bash()` needed — use direct `fetch`:

```js
// The CC proxies /apps/{slug}/api/... → localhost:{port}/api/...
// Port is read from data/port (written by the server at startup).
// Use AlfSDK.api() for automatic Bearer auth in sandboxed iframes.

// GET items
AlfSDK.api('/apps/my-app/api/items')
  .then(function(items) { render(items); });

// POST new item
AlfSDK.api('/apps/my-app/api/items', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ title: 'New item' })
}).then(function(item) { addToList(item); });

// DELETE
AlfSDK.api('/apps/my-app/api/items/123', { method: 'DELETE' });
```

All HTTP methods are proxied. Auth is handled by Bearer token (set automatically by `AlfSDK.api()`).
**Do not use `fetch()` directly** — sandboxed iframes have no cookies, so raw fetch calls will get 401.
If the server is not running, the proxy returns `502 Bad Gateway`.

---

## Lucide icon inline pattern

```js
// Example: trash icon
var trashIcon = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>';
```

Use `currentColor` for stroke/fill so icons inherit text color from CSS.
