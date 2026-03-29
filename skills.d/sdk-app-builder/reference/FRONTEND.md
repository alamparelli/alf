# Frontend (AlfSDK) -- Reference

All apps with a web UI use **AlfSDK** for theme sync, toasts, and parent SPA communication.
Vanilla JS only -- no frameworks, no build step, CSP-safe.

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
  <style>
    body {
      padding: 1.5rem;
      font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    }
    .card {
      background: var(--bg-card); border: 1px solid var(--border);
      border-radius: var(--radius, 8px); padding: 1.25rem; margin-bottom: 1rem;
    }
    .btn {
      display: inline-flex; align-items: center; gap: 6px;
      padding: 6px 14px; border: 1px solid var(--border);
      border-radius: var(--radius, 8px); background: var(--bg-input);
      color: var(--text); font-family: inherit; font-size: 0.85rem; cursor: pointer;
    }
    .btn-primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
    .btn:disabled { opacity: 0.5; cursor: not-allowed; }
    .form-row { margin-bottom: 0.75rem; }
    .form-row label { display: block; font-size: 0.8rem; font-weight: 500; margin-bottom: 4px; }
    .form-row input, .form-row textarea {
      width: 100%; padding: 8px; border: 1px solid var(--border);
      border-radius: var(--radius, 8px); background: var(--bg-input);
      color: var(--text); font-family: inherit; font-size: 0.85rem;
    }
    .form-row textarea { min-height: 100px; resize: vertical; }
    .actions { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 0.75rem; }
    .empty { color: var(--text-dim); padding: 2rem; text-align: center; }
  </style>
</head>
<body>
  <h2>My App</h2>
  <div id="list"></div>
  <div class="actions">
    <button class="btn btn-primary" onclick="showEditor()">Add Item</button>
  </div>

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
        el.innerHTML = '<p class="empty">No items yet.</p>';
        return;
      }
      el.innerHTML = items.map(function(item) {
        return '<div class="card"><strong>' + esc(item.name) + '</strong></div>';
      }).join('');
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
  onVisible, onHidden })               //   Lifecycle: tab visible/hidden callbacks
AlfSDK.tool(action, args)              // Run CLI tool. Returns Promise<string>.
AlfSDK.api(path, opts)                 // Authenticated fetch (same-origin cookies).
AlfSDK.bash(cmd)                       // Execute shell command via /api/bash.
AlfSDK.navigate(view)                  // Navigate parent SPA ('chat', 'settings').
AlfSDK.toast(msg, type)                // Toast in parent: 'success', 'error', 'info'.
AlfSDK.getTheme()                      // Returns { palette, dark }.

// Storage (server-side, persists across updates)
AlfSDK.storage.get(key?)               // Get value or full store
AlfSDK.storage.set(key, value)         // Set value (or pass object for batch)
AlfSDK.storage.remove(key)             // Delete key
AlfSDK.storage.clear()                 // Clear all
AlfSDK.storage.keys()                  // List all keys → Promise<string[]>
AlfSDK.storage.entries()               // List all entries → Promise<{key,value}[]>

// Events (auto-namespaced by slug)
AlfSDK.events.on(event, handler)       // Listen (bare name = own app, 'slug:event' = cross-app)
AlfSDK.events.off(event, handler)      // Unsubscribe
AlfSDK.events.emit(event, data)        // Emit (auto-prefixed with slug)
```

---

## CSS variables (theme)

Only use these -- never hardcode colors:

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
| `--red` | Error / negative |
| `--yellow` | Warning |
| `--mauve` | Purple accent |
| `--sapphire` | Blue accent |

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

1. **Always init AlfSDK** at the top of the script block
2. **Always include `onThemeChange`** to sync theme from parent SPA
3. **Set `font-family` explicitly** -- `system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif`. Google Fonts are blocked by CSP in iframes.
4. **Load `/static/style.css`** + `/static/theme-*.css` + `/static/theme-init.js`
5. **No external scripts/stylesheets** -- CSP blocks them
6. **No `unsafe-eval`** -- no Vue, Angular, Petite Vue
7. **Inline `<style>` only** -- no external CSS files you create
8. **Lucide SVG icons** -- inline SVG from lucide.dev. No icon fonts. No emoji as icons (unless user asks).
9. **XSS protection** -- always escape user content with a `div.textContent` wrapper (the `esc()` helper above)
10. **`font-family: inherit`** is NOT sufficient -- always set explicitly (see rule 3)

---

## Lucide icon inline pattern

```js
// Example: trash icon
var trashIcon = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14H6L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>';
```

Use `currentColor` for stroke/fill so icons inherit text color from CSS.
