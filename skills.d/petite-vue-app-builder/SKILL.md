---
name: petite-vue-app-builder
description: Build ALF apps with Petite Vue + AlfSDK for reactive UIs — use this instead of vanilla JS for new apps with interactive frontends
version: "1"
triggers: petite vue app, reactive app, vue app, interactive app, petite-vue
tier: sonnet
---

# Petite Vue App Builder

This skill extends the standard `app-builder` skill with **Petite Vue** for reactive frontends and the **AlfSDK** for parent SPA communication.

Use this when the app needs an interactive UI with reactive state. For simple static UIs, the standard `app-builder` skill is fine.

## When to use Petite Vue vs Vanilla JS

| Use Petite Vue | Use Vanilla JS (app-builder) |
|---|---|
| Lists with add/edit/delete | Static display pages |
| Forms with validation | Simple single-action buttons |
| Real-time updates | Minimal interactivity |
| Multiple views/tabs | One-page display |

## Frontend Template

Every Petite Vue app follows this template. The **AlfSDK** handles API calls, tool invocation, theme sync, and parent navigation.

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My App</title>
  <link rel="stylesheet" id="alf-theme" href="/static/theme-sage.css">
  <script src="/static/alf-app-sdk.js"></script>
  <script src="https://unpkg.com/petite-vue@0.4.1/dist/petite-vue.iife.js"></script>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: 'Work Sans', -apple-system, BlinkMacSystemFont, sans-serif;
      background: var(--bg); color: var(--text);
      padding: 1rem; line-height: 1.6;
    }
    h2 { font-family: 'Sora', sans-serif; margin-bottom: 1rem; }
    .card {
      background: var(--bg-card); border: 1px solid var(--border);
      border-radius: var(--radius, 8px); padding: 1.25rem; margin-bottom: 1rem;
    }
    .btn {
      display: inline-flex; align-items: center; gap: 6px;
      padding: 6px 14px; border: 1px solid var(--border);
      border-radius: var(--radius, 8px); background: var(--bg-input);
      color: var(--text); font-family: inherit; font-size: 0.85rem;
      cursor: pointer; transition: background 0.15s;
    }
    .btn:hover { background: var(--border); }
    .btn-primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
    .btn-danger { background: var(--red); color: #fff; border-color: var(--red); }
    .btn:disabled { opacity: 0.5; cursor: not-allowed; }
    .input {
      width: 100%; padding: 8px 12px; border: 1px solid var(--border);
      border-radius: var(--radius, 8px); background: var(--bg-input);
      color: var(--text); font-family: inherit; font-size: 0.85rem;
    }
    .loading { color: var(--text-dim); font-size: 0.85rem; }
    .empty { color: var(--text-dim); text-align: center; padding: 2rem; }
    /* app-specific styles below */
  </style>
</head>
<body>
  <div v-scope="App()" @vue:mounted="init">
    <h2>{{ title }}</h2>

    <div class="card" v-if="loading">
      <div class="loading">Loading...</div>
    </div>

    <div class="card" v-else-if="items.length === 0">
      <div class="empty">No items yet. Add one below.</div>
    </div>

    <div class="card" v-else>
      <div v-for="item in items" :key="item.id" style="display:flex;align-items:center;justify-content:space-between;padding:6px 0;border-bottom:1px solid var(--border)">
        <span>{{ item.name }}</span>
        <button class="btn btn-danger" @click="remove(item.id)" style="padding:3px 8px;font-size:0.75rem">Delete</button>
      </div>
    </div>

    <div class="card">
      <div style="display:flex;gap:8px">
        <input class="input" v-model="newName" placeholder="New item..." @keydown.enter="create" style="flex:1">
        <button class="btn btn-primary" @click="create" :disabled="!newName.trim()">Add</button>
      </div>
    </div>
  </div>

  <script>
    var SLUG = 'my-app'; // REPLACE with actual slug

    function App() {
      return {
        title: 'My App',
        items: [],
        newName: '',
        loading: true,

        init() {
          AlfSDK.init({
            slug: SLUG,
            onThemeChange: function(palette) {
              var link = document.getElementById('alf-theme');
              if (link) link.href = '/static/theme-' + palette + '.css';
            }
          });
          this.load();
        },

        load() {
          var self = this;
          AlfSDK.tool('list').then(function(out) {
            try { self.items = JSON.parse(out); } catch(e) { self.items = []; }
            self.loading = false;
          }).catch(function() { self.loading = false; });
        },

        create() {
          if (!this.newName.trim()) return;
          var self = this;
          var name = this.newName.trim();
          this.newName = '';
          AlfSDK.tool('create', { name: name }).then(function() {
            self.load();
          }).catch(function(e) { AlfSDK.toast(e.message, 'error'); });
        },

        remove(id) {
          if (!confirm('Delete this item?')) return;
          var self = this;
          AlfSDK.tool('delete', { id: id }).then(function() {
            self.load();
          }).catch(function(e) { AlfSDK.toast(e.message, 'error'); });
        },

      };
    }

    PetiteVue.createApp().mount();
  </script>
</body>
</html>
```

## AlfSDK Reference

The SDK is loaded from `/static/alf-app-sdk.js` (served by the parent SPA). Available methods:

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

1. **Always use AlfSDK.tool()** for backend calls — never raw `fetch('/api/bash')` with manual command building
2. **CRITICAL: Use `@vue:mounted="init"` on the root element** — NOT `$mounted` in the scope object. Petite Vue does NOT support `$mounted` as a scope property. The `init()` function must be defined in the scope and referenced via `@vue:mounted`.
3. **Always include onThemeChange** to sync theme from parent
4. **Use CSS variables** from the theme (`--bg`, `--text`, `--accent`, `--border`, etc.) — never hardcode colors
5. **No build step** — single HTML file, loads Petite Vue + SDK from CDN/static
6. **Petite Vue v0.4.1** — use `v-scope`, `v-if`, `v-for`, `v-model`, `@click`, `@keydown.enter`, `:disabled`, `{{ }}` interpolation
7. **Backend is still appsdk Go binary** — this skill only changes the frontend approach, not the backend

## Integration with app-builder

This skill replaces ONLY the frontend section of the app-builder workflow. The backend (Go CLI binary with appsdk, manifest.json, app.json, directory structure) follows the exact same pattern as the standard `app-builder` skill. Refer to that skill for:

- Go binary template (appsdk)
- REST server template
- manifest.json / app.json structure
- Build & install commands
- Marketplace publishing checklist
