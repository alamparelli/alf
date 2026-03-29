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

## AlfSDK v2 reference

### Core
```js
AlfSDK.init({ slug, onThemeChange })  // Init. Call once on load. REQUIRED.
AlfSDK.tool(action, args)             // Run CLI tool. Returns Promise<string>.
AlfSDK.api(path, opts)                // Authenticated fetch (same-origin cookies).
AlfSDK.bash(cmd)                      // Execute shell command via /api/bash.
AlfSDK.navigate(view)                 // Navigate parent SPA ('chat', 'settings').
AlfSDK.toast(msg, type)               // Toast in parent: 'success', 'error', 'info'.
AlfSDK.getTheme()                     // Returns { palette, dark }.
```

### Audio (ALWAYS use this — never create your own AudioContext)
```js
AlfSDK.audio.load(url)               // Load & cache audio file → Promise<AudioBuffer>
AlfSDK.audio.play(buffer, opts)      // Play buffer. opts: { volume: 0-1, loop: bool }. Returns source node.
AlfSDK.audio.playUrl(url, opts)      // Load + play in one call.
AlfSDK.audio.onUnlock(cb)            // Callback when audio is unlocked (after first user gesture).
AlfSDK.audio.getContext()            // Get shared AudioContext (for advanced audio).
AlfSDK.audio.isUnlocked()            // True if audio ready.
```

### Storage (persistent key/value, server-side)
```js
AlfSDK.storage.get()                 // Get all keys → Promise<object>
AlfSDK.storage.get('key')            // Get single value → Promise<any>
AlfSDK.storage.set('key', value)     // Set single key
AlfSDK.storage.set({ k1: v1, k2: v2 }) // Set multiple keys
AlfSDK.storage.remove('key')         // Delete key
AlfSDK.storage.clear()               // Clear all app storage
```

### Dialogs (native CC modals — bottom sheet on mobile)
```js
AlfSDK.confirm(msg, opts)            // → Promise<boolean>. opts: { title, confirmText, cancelText }
AlfSDK.prompt(msg, opts)             // → Promise<string|null>. opts: { title, defaultValue, placeholder, confirmText }
AlfSDK.sheet(html)                   // Show static HTML in CC bottom sheet / modal
AlfSDK.sheet(html, actions)          // Interactive sheet — actions map: { name: fn(params) }
                                     // HTML uses data-action="name" data-foo="bar" on clickable elements
AlfSDK.updateSheet(html)             // Update open sheet content (keeps action handlers)
AlfSDK.closeSheet()                  // Close current sheet
```

### Events (inter-app pub/sub)
```js
AlfSDK.events.on(event, handler)     // Subscribe to event from other apps
AlfSDK.events.off(event, handler)    // Unsubscribe
AlfSDK.events.emit(event, data)      // Broadcast to all other apps
```

### Viewport
```js
AlfSDK.viewport.isMobile()           // True if width <= 768px
AlfSDK.viewport.isPWA()              // True if standalone PWA
AlfSDK.viewport.orientation()        // 'portrait' or 'landscape'
AlfSDK.viewport.size()               // { width, height }
AlfSDK.viewport.safeArea()           // { top, bottom, left, right } insets
AlfSDK.viewport.onChange(cb)         // Callback on resize/rotation: cb({ mobile, orientation, size })
```

### Haptics
```js
AlfSDK.haptics.tap()                 // Light tap (10ms) — button presses
AlfSDK.haptics.notify()              // Double pulse — notifications
AlfSDK.haptics.success()             // Rising pattern — success actions
AlfSDK.haptics.error()               // Heavy buzz — error feedback
AlfSDK.haptics.vibrate([100,50,200]) // Custom pattern
AlfSDK.haptics.isAvailable()         // True if device supports vibration
```

### Clipboard
```js
AlfSDK.clipboard.write(text)         // Copy to clipboard → Promise
AlfSDK.clipboard.read()              // Read from clipboard → Promise<string>
```

### I18n
```js
AlfSDK.i18n.locale()                 // Full locale: 'en-US', 'fr-FR'
AlfSDK.i18n.lang()                   // Language: 'en', 'fr'
AlfSDK.i18n.dir()                    // 'ltr' or 'rtl'
AlfSDK.i18n.languages()              // All preferred languages
```

### Badge
```js
AlfSDK.badge.set(count)              // Set badge on sidebar icon
AlfSDK.badge.increment()             // Add 1
AlfSDK.badge.clear()                 // Remove badge
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
