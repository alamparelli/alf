---
name: sdk-app-builder
description: Build ALF apps with AlfSDK for clean frontends — use this for new apps that need the SDK communication layer with the parent SPA
version: "2"
triggers: sdk app, new app with sdk, app with theme, interactive app
tier: sonnet
---

# SDK App Builder

This skill extends the standard `app-builder` skill with the **AlfSDK** for parent SPA communication (theme sync, toast, navigation). Uses **vanilla JS** — no framework, no build step, CSP-safe.

## Frontend Template

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
    body { padding: 1.5rem; }
    .card { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius, 8px); padding: 1.25rem; margin-bottom: 1rem; }
    .btn { display: inline-flex; align-items: center; gap: 6px; padding: 6px 14px; border: 1px solid var(--border); border-radius: var(--radius, 8px); background: var(--bg-input); color: var(--text); font-family: inherit; font-size: 0.85rem; cursor: pointer; }
    .btn-primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
    .btn:disabled { opacity: 0.5; cursor: not-allowed; }
    .form-row { margin-bottom: 0.75rem; }
    .form-row label { display: block; font-size: 0.8rem; font-weight: 500; margin-bottom: 4px; }
    .form-row input, .form-row textarea { width: 100%; padding: 8px; border: 1px solid var(--border); border-radius: var(--radius, 8px); background: var(--bg-input); color: var(--text); font-family: inherit; font-size: 0.85rem; }
    .form-row textarea { min-height: 100px; resize: vertical; }
    .actions { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 0.75rem; }
    .empty { color: var(--text-dim); padding: 2rem; text-align: center; }
    /* app-specific styles below */
  </style>
</head>
<body>
  <h2>My App</h2>
  <div id="list"></div>
  <div id="editor" style="display:none"></div>
  <div class="actions" id="toolbar">
    <button class="btn btn-primary" onclick="showEditor()">Add Item</button>
  </div>

  <script>
    var SLUG = 'my-app'; // REPLACE with actual slug

    AlfSDK.init({
      slug: SLUG,
      onThemeChange: function(palette) {
        var link = document.getElementById('alf-theme');
        if (link) link.href = '/static/theme-' + palette + '.css';
      }
    });

    var items = [];

    function load() {
      AlfSDK.tool('list').then(function(out) {
        try { items = JSON.parse(out); } catch(e) { items = []; }
        renderList();
      }).catch(function(e) {
        document.getElementById('list').innerHTML = '<p class="empty">Error: ' + esc(e.message) + '</p>';
      });
    }

    function renderList() {
      var el = document.getElementById('list');
      if (!items || !items.length) {
        el.innerHTML = '<p class="empty">No items yet.</p>';
        return;
      }
      el.innerHTML = items.map(function(item) {
        return '<div class="card" onclick="editItem(' + item.id + ')">' +
          '<strong>' + esc(item.name) + '</strong>' +
          '</div>';
      }).join('');
    }

    function showEditor(item) {
      document.getElementById('list').style.display = 'none';
      document.getElementById('toolbar').style.display = 'none';
      var ed = document.getElementById('editor');
      ed.style.display = '';
      var id = item ? item.id : 0;
      ed.innerHTML =
        '<div class="form-row"><label>Name</label><input id="fName" value="' + esc(item ? item.name : '') + '"></div>' +
        '<div class="actions">' +
          '<button class="btn btn-primary" onclick="save(' + id + ')">Save</button>' +
          '<button class="btn" onclick="backToList()">Cancel</button>' +
          (id ? '<button class="btn" style="color:var(--red)" onclick="remove(' + id + ')">Delete</button>' : '') +
        '</div>';
    }

    function backToList() {
      document.getElementById('editor').style.display = 'none';
      document.getElementById('list').style.display = '';
      document.getElementById('toolbar').style.display = '';
      load();
    }

    function editItem(id) {
      var item = items.find(function(x) { return x.id === id; });
      if (item) showEditor(item);
    }

    function save(id) {
      var name = document.getElementById('fName').value.trim();
      if (!name) { AlfSDK.toast('Name required', 'error'); return; }
      var action = id ? 'update' : 'create';
      var args = { name: name };
      if (id) args.id = String(id);
      AlfSDK.tool(action, args).then(function() {
        AlfSDK.toast('Saved', 'success');
        backToList();
      }).catch(function(e) { AlfSDK.toast(e.message, 'error'); });
    }

    function remove(id) {
      if (!confirm('Delete this item?')) return;
      AlfSDK.tool('delete', { id: String(id) }).then(function() {
        AlfSDK.toast('Deleted');
        backToList();
      }).catch(function(e) { AlfSDK.toast(e.message, 'error'); });
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

## AlfSDK Reference

The SDK is loaded from `/static/alf-app-sdk.js`. Available methods:

| Method | Description |
|---|---|
| `AlfSDK.init({ slug, onThemeChange })` | Initialize. Call once on load. |
| `AlfSDK.tool(action, args)` | Run CLI tool with action + args. Returns output string. |
| `AlfSDK.api(path, opts)` | Authenticated fetch (same-origin cookies). |
| `AlfSDK.bash(cmd)` | Execute shell command via `/api/bash`. |
| `AlfSDK.navigate(view)` | Navigate parent SPA (e.g. `'chat'`, `'settings'`). |
| `AlfSDK.toast(msg, type)` | Show toast in parent (`'success'`, `'error'`, `'info'`). |
| `AlfSDK.getTheme()` | Returns `{ palette, dark }`. |

## Key Rules

1. **Always use AlfSDK.tool()** for backend calls — never raw `fetch('/api/bash')`
2. **Always init AlfSDK** at the top of your script
3. **Always include onThemeChange** to sync theme from parent
4. **Use CSS variables** from the theme (`--bg`, `--text`, `--accent`, `--border`, etc.) — never hardcode colors
5. **Load `/static/style.css`** for base styles and `/static/theme-*.css` for theme colors
6. **Load `/static/theme-init.js`** for FOUC prevention
7. **No build step** — single HTML file, vanilla JS only
8. **No `unsafe-eval`** — do NOT use frameworks that require `new Function()` (Petite Vue, Vue, Angular). CSP blocks them.
9. **Backend is still appsdk Go binary** — this skill only adds the SDK frontend layer

## Integration with app-builder

This skill replaces ONLY the frontend section of the app-builder workflow. Refer to the standard `app-builder` skill for:

- Go binary template (appsdk)
- REST server template
- manifest.json / app.json structure
- Build & install commands
- Marketplace publishing checklist
