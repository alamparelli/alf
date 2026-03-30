# Frontend (AlfSDK) -- Reference

All apps with a web UI use **AlfSDK** for theme sync, toasts, and parent SPA communication.
Vanilla JS only -- no frameworks, no build step, CSP-safe.

> **Design system**: `alf-ui.css` is auto-injected into all app iframes. Use its classes (`.btn`, `.card`, `.input`, `.form-group`, etc.) instead of writing inline styles. See `AIG.md` for the full component reference.

---

## Full template

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My App</title>
  <link rel="stylesheet" href="/static/style.css">
  <link rel="stylesheet" id="alf-theme" href="/static/theme-sage.css">
  <script src="/static/theme-init.js"></script>
  <script src="/static/alf-app-sdk.js"></script>
  <!-- alf-ui.css is auto-injected by the parent frame — no need to import -->
  <style>
    body {
      padding: var(--space-lg, 24px);
      max-width: 760px;
      margin: 0 auto;
      font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
      background: var(--bg);
      color: var(--text);
      line-height: 1.5;
    }
    h2 { margin-bottom: var(--space-md, 16px); }
  </style>
</head>
<body>
  <div class="flex justify-between items-center mb-md">
    <h2 class="m-0">My App</h2>
    <button class="btn btn-primary btn-sm" onclick="showEditor()">Add Item</button>
  </div>

  <div id="list"></div>

  <script>
    AlfSDK.init({
      slug: 'my-app',
      onThemeChange: function(palette) {
        document.getElementById('alf-theme').href = '/static/theme-' + palette + '.css';
      }
    });

    var items = [];

    // CLI tool app: use AlfSDK.tool()
    function load() {
      AlfSDK.tool('list').then(function(out) {
        try { items = JSON.parse(out); } catch(e) { items = []; }
        render();
      });
    }

    // REST server app: use direct fetch
    // function load() {
    //   fetch('/apps/my-app/api/items').then(r => r.json()).then(function(data) {
    //     items = data; render();
    //   });
    // }

    function render() {
      var el = document.getElementById('list');
      if (!items || !items.length) {
        el.innerHTML = '<div class="empty-state"><p>No items yet.</p></div>';
        return;
      }
      el.innerHTML = '<div class="card"><ul class="list">' + items.map(function(item) {
        return '<li class="list-item"><strong>' + esc(item.name) + '</strong></li>';
      }).join('') + '</ul></div>';
    }

    function esc(s) {
      if (!s) return '';
      var d = document.createElement('div');
      d.textContent = s;
      return d.innerHTML;
    }

    load();
  </script>
</body>
</html>
```

---

## AlfSDK reference

```js
// Core
AlfSDK.VERSION                         // '3.0.0'
AlfSDK.init({ slug, onThemeChange,     // Init. Call once on load. REQUIRED.
  onVisible, onHidden, onDestroy })    //   Lifecycle callbacks (see Lifecycle section)
AlfSDK.tool(action, args)              // Run CLI tool. Returns Promise<string>.
AlfSDK.api(path, opts)                 // Authenticated fetch (same-origin cookies).
AlfSDK.bash(cmd)                       // Execute shell command via /api/bash.
AlfSDK.navigate(view)                  // Navigate parent SPA ('chat', 'settings', 'vault').
AlfSDK.toast(msg, type)                // Toast in parent: 'success', 'error', 'info'.
AlfSDK.getTheme()                      // Returns { palette, dark }.
AlfSDK.confirm(message, opts?)         // Confirmation dialog → Promise<boolean>
AlfSDK.prompt(message, opts?)          // Input dialog → Promise<string|null>
                                       //   opts: { title, placeholder, ok, cancel, multiline }

// Sheets (bottom-sheet modals rendered in parent)
AlfSDK.sheet(html, actions?)           // Show HTML in bottom-sheet modal
                                       //   actions: [{ label, style?, callback(params) }]
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

Only use these -- never hardcode colors. Full reference in `AIG.md`.

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

## Rules

1. **Follow AIG** -- use `alf-ui.css` classes for buttons, cards, forms, lists. See `AIG.md`.
2. **Always init AlfSDK** at the top of the script block
3. **Always include `onThemeChange`** to sync theme from parent SPA
4. **Set `font-family` explicitly** -- `system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`. Google Fonts are blocked by CSP in iframes.
5. **Load `/static/style.css`** + `/static/theme-*.css` + `/static/theme-init.js`
6. **No external scripts/stylesheets** -- CSP blocks them. `alf-ui.css` is auto-injected.
7. **No `unsafe-eval`** -- no Vue, Angular, Petite Vue
8. **Inline `<style>` only** -- no external CSS files you create. Minimize inline styles — prefer `alf-ui.css` classes.
9. **Lucide SVG icons** -- inline SVG from lucide.dev. No icon fonts. No emoji as icons (unless user asks).
10. **XSS protection** -- always escape user content with a `div.textContent` wrapper (the `esc()` helper above)
11. **`font-family: inherit`** is NOT sufficient -- always set explicitly (see rule 4)
12. **Use spacing tokens** -- `--space-xs` to `--space-xl` or classes `.gap-sm`, `.p-md`, `.mb-lg` etc.

---

## Lifecycle hooks

```js
AlfSDK.init({
  slug: 'my-app',
  onThemeChange: function(palette) { /* theme switched */ },
  onVisible: function() { /* tab/app became visible — resume polling, animations */ },
  onHidden: function() { /* tab/app hidden — pause work, save state */ },
  onDestroy: function() { /* app being torn down — cleanup */ }
});
```

- `onVisible`/`onHidden` fire on Page Visibility API changes and when the parent SPA switches tabs.
- `onDestroy` fires when the iframe is removed from the DOM.
- The SDK blocks `tool()`, `bash()`, `storage.*` calls until `init()` completes.

---

## Sheets (bottom-sheet modals)

Sheets render HTML in a parent-level modal (bottom-sheet on mobile, centered on desktop).
HTML is sanitized -- safe tags, attributes (including `style`), and `data-*` attributes are preserved. No scripts.

```js
// Simple informational sheet
AlfSDK.sheet('<h3>Details</h3><p>Some content here</p>');

// Sheet with action buttons
AlfSDK.sheet(
  '<h3>Confirm Delete</h3><p>This cannot be undone.</p>',
  [
    { label: 'Cancel', callback: function() { AlfSDK.closeSheet(); } },
    { label: 'Delete', style: 'background:var(--red);color:#fff', callback: function() {
      doDelete();
      AlfSDK.closeSheet();
    }}
  ]
);

// Sheet with form inputs — values auto-collected by name attribute
AlfSDK.sheet(
  '<h3>Edit Item</h3>' +
  '<input name="title" value="Current Title" style="width:100%;padding:8px;margin:8px 0">' +
  '<textarea name="notes" style="width:100%;padding:8px" rows="3"></textarea>' +
  '<button data-action="save" style="padding:8px 16px;background:var(--accent);color:var(--on-accent);border:none;border-radius:6px;cursor:pointer">Save</button>',
  [{ label: 'save', callback: function(params) {
    // params = { title: '...', notes: '...' }
    saveItem(params);
    AlfSDK.closeSheet();
  }}]
);

// Update sheet content dynamically
AlfSDK.updateSheet('<h3>Loading...</h3><p>Please wait</p>');
```

**Button click handling**: Elements with `data-action="name"` trigger the matching action callback.
Form inputs with `name` or `data-field` attributes are collected into the `params` object.

---

## Permissions (marketplace apps)

Marketplace apps declare permissions in `manifest.json`:

```json
{ "permissions": ["storage", "bash", "clipboard"] }
```

| Permission | What it grants | Untrusted apps |
|---|---|---|
| `storage` | Server-side key/value storage | Allowed |
| `events` | Cross-app event emission | Allowed |
| `clipboard` | Read/write clipboard | Allowed |
| `bash` | Shell command execution | Denied |
| `upload` | File uploads | Denied |
| `network` | Network access in sandboxed bash | Denied |

- **Local/default apps** (not installed from marketplace) have all permissions.
- **Untrusted apps** are capped to `storage`, `events`, `clipboard` regardless of what they declare.
- **Trusted apps** (verified in registry) can use all permissions.
- APIs that require a permission return `403` with `{"error": "permission denied: <perm>"}`.

---

## REST server apps (API proxy)

Apps with a backend server (`service.json`) get automatic API proxying.
No `AlfSDK.bash()` needed — use direct `fetch`:

```js
// The CC proxies /apps/{slug}/api/... → localhost:{port}/api/...
// Port is read from data/port (written by the server at startup).

// GET items
fetch('/apps/my-app/api/items', { credentials: 'same-origin' })
  .then(function(r) { return r.json(); })
  .then(function(items) { render(items); });

// POST new item
fetch('/apps/my-app/api/items', {
  method: 'POST',
  credentials: 'same-origin',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ title: 'New item' })
}).then(function(r) { return r.json(); });

// DELETE
fetch('/apps/my-app/api/items/123', {
  method: 'DELETE',
  credentials: 'same-origin'
});
```

All HTTP methods are proxied. Auth cookies are **not** forwarded to the app server.
If the server is not running, the proxy returns `502 Bad Gateway`.

---

## Lucide icon inline pattern

```js
// Example: trash icon
var trashIcon = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>';
```

Use `currentColor` for stroke/fill so icons inherit text color from CSS.
