---
category: Development
tags: SDK, apps, marketplace, API, audio, storage, events, mobile
order: 16
---

# App SDK Reference

Complete reference for `AlfSDK` — the JavaScript SDK available to all marketplace apps running in the Control Center.

## Setup

Include the SDK in your app's `index.html`:

```html
<script src="/static/alf-app-sdk.js"></script>
<script>
  AlfSDK.init({
    slug: 'my-app',
    onThemeChange: function(palette, isDark) {
      // Update your app's theme
    }
  });
</script>
```

The SDK auto-handles mobile audio unlock, viewport detection, and parent communication.

## Core

### `AlfSDK.init(opts)`

Initialize the SDK. Call once on page load.

| Option | Type | Description |
|--------|------|-------------|
| `slug` | string | **Required.** Your app's slug (matches directory name) |
| `onThemeChange` | function | Called with `(palette, isDark)` when user changes theme |
| `onDestroy` | function | Called when app is being unloaded |
| `onVisible` | function | Called when app tab becomes visible (browser or CC tab switch) |
| `onHidden` | function | Called when app tab becomes hidden |

### `AlfSDK.api(path, opts)`

Authenticated API call. Session cookies are handled automatically.

```js
// GET
AlfSDK.api('/api/vault/secrets').then(function(data) { ... });

// POST
AlfSDK.api('/api/bash', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ command: 'ls' })
});
```

### `AlfSDK.bash(cmd)`

Execute a shell command. Returns `{ output, exit_code, error }`.

```js
AlfSDK.bash('echo hello').then(function(res) {
  console.log(res.output); // "hello\n"
});
```

### `AlfSDK.tool(action, args)`

Run the app's CLI tool binary.

```js
AlfSDK.tool('list', { filter: 'active' }).then(function(output) {
  console.log(output);
});
```

### `AlfSDK.navigate(view)`

Navigate the Control Center to a view.

```js
AlfSDK.navigate('chat');         // System views
AlfSDK.navigate('page:my-app'); // Other apps
```

### `AlfSDK.toast(message, type)`

Show a notification toast. Types: `'success'`, `'error'`, `'info'`.

```js
AlfSDK.toast('Saved!', 'success');
```

### `AlfSDK.getTheme()`

Returns `{ palette: string, dark: boolean }` for the current theme.

---

## Audio

Handles mobile browser autoplay restrictions. The AudioContext is created and unlocked automatically on the first user gesture inside the iframe.

### `AlfSDK.audio.load(url)`

Load and decode an audio file. Returns a Promise for an AudioBuffer. Results are cached.

```js
var shotSound;
AlfSDK.audio.load('assets/shot.wav').then(function(buf) {
  shotSound = buf;
});
```

### `AlfSDK.audio.play(buffer, opts)`

Play a loaded AudioBuffer. Returns the AudioBufferSourceNode (call `.stop()` to stop).

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `volume` | number | 1.0 | Volume (0 to 1) |
| `loop` | boolean | false | Loop playback |

```js
var bgm = AlfSDK.audio.play(musicBuffer, { volume: 0.3, loop: true });
// Later: bgm.stop();
```

### `AlfSDK.audio.playUrl(url, opts)`

Convenience: load + play in one call.

```js
AlfSDK.audio.playUrl('assets/click.wav', { volume: 0.5 });
```

### `AlfSDK.audio.onUnlock(callback)`

Register a callback that fires once audio is unlocked (user has interacted). If already unlocked, fires immediately. Use this to preload sounds.

```js
AlfSDK.audio.onUnlock(function() {
  AlfSDK.audio.load('assets/bgm.mp3');
  AlfSDK.audio.load('assets/hit.wav');
});
```

### `AlfSDK.audio.getContext()`

Returns the shared AudioContext (or null before first gesture). Useful for advanced audio processing (filters, analyzers).

### `AlfSDK.audio.isUnlocked()`

Returns true if audio is ready to play.

---

## Sheet / Modal

Show content in the CC's native modal (centered on desktop, iOS-style bottom sheet on mobile with drag-to-dismiss).

### `AlfSDK.sheet(html, actions?)`

Show static HTML, or interactive HTML with action callbacks.

```js
// Static (read-only)
AlfSDK.sheet('<h3>About</h3><p>Version 1.0</p>');

// Interactive — use data-action attributes, all data-* are passed as params
AlfSDK.sheet(
  '<h3>Book Detail</h3>' +
  '<p>The Great Gatsby</p>' +
  '<div>' +
  '  <button data-action="rate" data-id="12" data-stars="5">★★★★★</button>' +
  '  <button data-action="delete" data-id="12" style="color:var(--red)">Delete</button>' +
  '</div>',
  {
    rate: function(p) {
      setRating(p.id, parseInt(p.stars));
      // Update the sheet in-place after state change
      AlfSDK.updateSheet(renderBookDetail(p.id));
    },
    delete: function(p) {
      deleteBook(p.id);
      AlfSDK.closeSheet();
    }
  }
);
```

**Form inputs**: Action handlers automatically collect values from `input`, `select`, and `textarea` elements that have a `name` or `data-field` attribute:

```js
AlfSDK.sheet(
  '<input name="date" type="date" value="2026-01-01" />' +
  '<textarea data-field="notes" placeholder="Notes..."></textarea>' +
  '<button data-action="save" data-id="12">Save</button>',
  {
    save: function(p) {
      // p.id = "12" (from data-id)
      // p.date = "2026-01-01" (from input[name="date"])
      // p.notes = "user text" (from textarea[data-field="notes"])
      updateBook(p.id, p.date, p.notes);
    }
  }
);
```

### `AlfSDK.updateSheet(html)`

Update the currently open sheet's content without closing it. The action callbacks from the original `sheet()` call are preserved.

```js
AlfSDK.updateSheet('<h3>Updated!</h3><p>New content</p>');
```

### `AlfSDK.closeSheet()`

Close the current sheet.

---

## Confirm / Prompt

Native CC dialogs that render as bottom sheets on mobile.

### `AlfSDK.confirm(message, opts)`

Returns a Promise that resolves to `true` (confirmed) or `false` (cancelled).

```js
AlfSDK.confirm('Delete this item?', {
  title: 'Confirm Delete',
  confirmText: 'Delete',
  cancelText: 'Keep'
}).then(function(ok) {
  if (ok) deleteItem();
});
```

### `AlfSDK.prompt(message, opts)`

Returns a Promise that resolves to the input string, or `null` if cancelled.

| Option | Type | Description |
|--------|------|-------------|
| `title` | string | Dialog title |
| `defaultValue` | string | Pre-filled input value |
| `placeholder` | string | Input placeholder |
| `confirmText` | string | Confirm button text |
| `multiline` | boolean | Render a textarea instead of single-line input |

```js
AlfSDK.prompt('Enter a name:', {
  title: 'Rename',
  defaultValue: 'Untitled',
  placeholder: 'Project name'
}).then(function(name) {
  if (name !== null) rename(name);
});
```

---

## Storage

Persistent key/value storage scoped to your app. Data is stored server-side as JSON, survives app updates.

### `AlfSDK.storage.get(key?)`

Get a single value or the entire store.

```js
// Single key
AlfSDK.storage.get('highScore').then(function(val) { ... });

// All keys
AlfSDK.storage.get().then(function(store) {
  console.log(store); // { highScore: 42, theme: 'dark' }
});
```

### `AlfSDK.storage.set(key, value)` or `AlfSDK.storage.set(object)`

Set one or more keys. Pass `null` as value to delete a key.

```js
// Single key
AlfSDK.storage.set('highScore', 42);

// Multiple keys
AlfSDK.storage.set({ highScore: 42, level: 5 });
```

### `AlfSDK.storage.remove(key)`

Delete a key.

### `AlfSDK.storage.clear()`

Clear all storage for this app.

### `AlfSDK.storage.keys()`

List all stored keys. Returns `Promise<string[]>`.

```js
AlfSDK.storage.keys().then(function(keys) {
  console.log(keys); // ['highScore', 'level', 'theme']
});
```

### `AlfSDK.storage.entries()`

List all entries as key/value pairs. Returns `Promise<Array<{key, value}>>`.

```js
AlfSDK.storage.entries().then(function(entries) {
  entries.forEach(function(e) { console.log(e.key, e.value); });
});
```

---

## Upload

### `AlfSDK.upload(file)`

Upload a file to the app's persistent storage. Returns `Promise<{path, name, size}>`.

```js
var input = document.querySelector('input[type=file]');
input.addEventListener('change', function() {
  AlfSDK.upload(input.files[0]).then(function(result) {
    console.log('Uploaded:', result.path, result.size, 'bytes');
  });
});
```

Files are stored at `data/uploads/` within the app's data directory. Max size: 10MB. Filenames are sanitized (path traversal characters stripped).

---

## Error Reporting

Errors are automatically captured and logged when the SDK is initialized. Both `window.onerror` and `unhandledrejection` events are forwarded to the daemon's per-app error log.

### Viewing errors

```
GET /api/apps/{slug}/errors → { errors: [...], count: N }
DELETE /api/apps/{slug}/errors → clears the log
```

The error log is a ring buffer (max 100 entries). Each entry contains `timestamp`, `message`, `stack`, and `source`.

---

## Events

Inter-app pub/sub with automatic slug namespacing. Events emitted by your app are prefixed with your slug (e.g. `my-app:updated`). Listening to a bare event name listens to your own app's events. Use `slug:event` format to listen to other apps.

### `AlfSDK.events.on(event, handler)`

```js
// Listen to own events (auto-prefixed with your slug)
AlfSDK.events.on('player-scored', function(data) {
  console.log(data.points);
});

// Listen to another app's events (explicit slug prefix)
AlfSDK.events.on('leaderboard:updated', function(data) {
  console.log('Leaderboard changed:', data);
});
```

### `AlfSDK.events.off(event, handler)`

Unsubscribe. Same namespacing rules as `on()`.

### `AlfSDK.events.emit(event, data)`

Emit an event (auto-prefixed with your slug, relayed to all other apps).

```js
// Emitted as 'my-app:player-scored' to other apps
AlfSDK.events.emit('player-scored', { points: 100 });
```

---

## Viewport

Device and screen information for responsive apps.

### `AlfSDK.viewport.isMobile()`

Returns `true` if viewport width <= 768px.

### `AlfSDK.viewport.isPWA()`

Returns `true` if running as installed PWA (standalone mode).

### `AlfSDK.viewport.orientation()`

Returns `'portrait'` or `'landscape'`.

### `AlfSDK.viewport.size()`

Returns `{ width, height }` in pixels.

### `AlfSDK.viewport.safeArea()`

Returns `{ top, bottom, left, right }` safe area insets (useful for notched devices).

### `AlfSDK.viewport.onChange(callback)`

Register a callback for resize/orientation changes. Callback receives `{ mobile, orientation, size }`.

```js
AlfSDK.viewport.onChange(function(info) {
  if (info.mobile) showMobileLayout();
  else showDesktopLayout();
});
```

---

## Haptics

Vibration feedback for mobile devices. No-op on devices without vibration support.

### `AlfSDK.haptics.tap()`

Light tap (10ms). Use for button presses.

### `AlfSDK.haptics.notify()`

Double pulse. Use for notifications.

### `AlfSDK.haptics.success()`

Rising pattern. Use for successful actions.

### `AlfSDK.haptics.error()`

Heavy buzz. Use for errors.

### `AlfSDK.haptics.vibrate(pattern)`

Custom pattern. Array of alternating vibrate/pause durations in ms.

```js
AlfSDK.haptics.vibrate([100, 50, 200]); // buzz, pause, buzz
```

### `AlfSDK.haptics.isAvailable()`

Returns `true` if the device supports vibration.

---

## Clipboard

Cross-frame clipboard access (delegates to the parent frame for permission).

### `AlfSDK.clipboard.write(text)`

```js
AlfSDK.clipboard.write('copied text').then(function() {
  AlfSDK.toast('Copied!');
});
```

### `AlfSDK.clipboard.read()`

```js
AlfSDK.clipboard.read().then(function(text) {
  console.log('Pasted:', text);
});
```

---

## I18n

User locale information for internationalization.

### `AlfSDK.i18n.locale()`

Full locale (e.g. `'en-US'`, `'fr-FR'`).

### `AlfSDK.i18n.lang()`

Language code (e.g. `'en'`, `'fr'`).

### `AlfSDK.i18n.dir()`

Text direction: `'ltr'` or `'rtl'`.

### `AlfSDK.i18n.languages()`

All preferred languages from `navigator.languages`.

---

## Badge

Set a notification badge on your app's icon in the sidebar.

### `AlfSDK.badge.set(count)`

```js
AlfSDK.badge.set(3); // Show "3" badge
```

### `AlfSDK.badge.increment()`

Add 1 to the current badge count.

### `AlfSDK.badge.clear()`

Remove the badge.

---

## Full Example: Game App

```html
<!DOCTYPE html>
<html>
<head>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <script src="/static/alf-app-sdk.js"></script>
</head>
<body>
<canvas id="game"></canvas>
<script>
  AlfSDK.init({
    slug: 'my-game',
    onThemeChange: function(palette, dark) {
      document.body.style.background = dark ? '#1a1a1a' : '#fff';
    }
  });

  // Preload audio
  var sounds = {};
  AlfSDK.audio.onUnlock(function() {
    AlfSDK.audio.load('assets/hit.wav').then(function(b) { sounds.hit = b; });
    AlfSDK.audio.load('assets/bgm.mp3').then(function(b) {
      sounds.bgm = AlfSDK.audio.play(b, { volume: 0.3, loop: true });
    });
  });

  // Load saved state
  AlfSDK.storage.get().then(function(data) {
    highScore = data.highScore || 0;
  });

  // Responsive layout
  AlfSDK.viewport.onChange(function(info) {
    resizeCanvas(info.size.width, info.size.height);
  });

  // Game over
  function gameOver(score) {
    AlfSDK.haptics.error();
    AlfSDK.audio.play(sounds.hit, { volume: 0.8 });
    if (score > highScore) {
      highScore = score;
      AlfSDK.storage.set('highScore', score);
      AlfSDK.toast('New high score: ' + score + '!');
    }
    AlfSDK.confirm('Play again?', { title: 'Game Over' }).then(function(ok) {
      if (ok) restart();
      else AlfSDK.navigate('home');
    });
  }
</script>
</body>
</html>
```
