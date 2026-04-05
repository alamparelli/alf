# Games -- Reference

Use this for canvas games, board games, and any interactive visual app.
Architecture: **REST server** (no CLI tool unless the LLM needs data access).
Also read `AIG.md` for the design system, `REST-SERVER.md` for the backend and `FRONTEND.md` for AlfSDK init.

> **Design system**: `alf-ui.css` is auto-injected into all app iframes. Use its classes (`.btn`, `.btn-primary`, etc.) for UI elements outside the canvas (overlays, menus, HUD buttons). See `AIG.md`.

---

## Canonical patterns (extracted from Bubble Shooter + Snake)

Bubble Shooter is the reference for responsive canvas games.
Snake is the reference for tick-based games with a d-pad.

---

## Audio (CRITICAL — use AlfSDK.audio)

**NEVER create your own `AudioContext`.** Mobile browsers (especially iOS Safari in iframes) block audio unless the context is created and resumed during a user gesture. `AlfSDK.audio` handles this automatically.

### Synthesized sounds (oscillator-based)

```js
// Preload: get the shared AudioContext after unlock
var audioCtx;
AlfSDK.audio.onUnlock(function() {
  audioCtx = AlfSDK.audio.getContext();
});

function playTone(freq, dur, type, vol) {
  if (!audioCtx) return;
  var osc = audioCtx.createOscillator();
  var g = audioCtx.createGain();
  osc.type = type || 'square';
  osc.frequency.value = freq;
  g.gain.setValueAtTime(vol || 0.3, audioCtx.currentTime);
  g.gain.exponentialRampToValueAtTime(0.001, audioCtx.currentTime + dur);
  osc.connect(g);
  g.connect(audioCtx.destination);
  osc.start();
  osc.stop(audioCtx.currentTime + dur);
}

// Sound effects
function sfxPop()    { playTone(600, 0.08, 'square', 0.2); }
function sfxBounce() { playTone(220, 0.04, 'square', 0.1); }
function sfxShoot()  { playTone(350, 0.06, 'triangle', 0.2); }
```

### Audio file playback

```js
var sounds = {};
AlfSDK.audio.onUnlock(function() {
  AlfSDK.audio.load('assets/hit.wav').then(function(b) { sounds.hit = b; });
  AlfSDK.audio.load('assets/bgm.mp3').then(function(b) {
    sounds.bgm = AlfSDK.audio.play(b, { volume: 0.3, loop: true });
  });
});

function sfxHit() { AlfSDK.audio.play(sounds.hit, { volume: 0.8 }); }
```

### Mute toggle

```js
var muted = false;
var masterGain;
AlfSDK.audio.onUnlock(function() {
  var ctx = AlfSDK.audio.getContext();
  masterGain = ctx.createGain();
  masterGain.connect(ctx.destination);
  // Route all audio through masterGain instead of ctx.destination
});

function toggleMute() {
  muted = !muted;
  if (masterGain) masterGain.gain.value = muted ? 0 : 1;
}
```

### High score persistence (use SDK storage)

```js
var highScore = 0;
AlfSDK.storage.get('highScore').then(function(val) {
  highScore = val || 0;
});

function updateBest(score) {
  if (score > highScore) {
    highScore = score;
    AlfSDK.storage.set('highScore', score);
    AlfSDK.toast('New high score: ' + score + '!');
  }
}
```

---

## Meta viewport (REQUIRED)

```html
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
```

`viewport-fit=cover` handles notches and safe areas on iOS.

---

## Body layout

```css
html, body {
  height: 100%; height: 100dvh; /* dvh = dynamic viewport height (handles mobile URL bar) */
  overflow: hidden;              /* prevent scroll during gameplay */
  font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  background: var(--bg);
  color: var(--text);
}
#app {
  display: flex;
  flex-direction: column;
  align-items: center;
  height: 100%;
  /* safe area insets for iPhone notch / home bar */
  padding:
    env(safe-area-inset-top, 0)
    env(safe-area-inset-right, 0)
    env(safe-area-inset-bottom, 0)
    env(safe-area-inset-left, 0);
}
```

---

## Canvas container

```css
#canvas-wrap {
  position: relative;
  flex: 1 1 auto;      /* fill remaining vertical space after HUD */
  min-height: 0;       /* REQUIRED: prevents flex overflow */
  width: 100%;
  max-width: 480px;    /* constrain to mobile-like width on desktop */
  touch-action: none;  /* disable all browser touch gestures on canvas */
}
canvas {
  display: block;
  width: 100%;
  height: 100%;
}
```

**Never set fixed pixel dimensions on the canvas element in CSS.**
The canvas CSS size fills the container; the JS `resize()` function sets the internal resolution.

---

## resize() function (REQUIRED for canvas games)

Never compute dimensions once at load. Always use a `resize()` function:

```js
var canvas = document.getElementById('c');
var ctx    = canvas.getContext('2d');
var wrap   = document.getElementById('canvas-wrap');
var CW, CH; // internal canvas resolution

function resize() {
  var rect = wrap.getBoundingClientRect();
  CW = Math.floor(rect.width);
  CH = Math.floor(rect.height);
  canvas.width  = CW;
  canvas.height = CH;
  // recompute any dimension-dependent constants here
  // e.g. CELL = Math.floor(CW / COLS);
  refreshColors(); // re-read CSS vars after resize (theme may have changed)
  if (typeof grid !== 'undefined') draw(); // redraw if game is initialized
}

window.addEventListener('resize', resize);

// Double rAF: wait for layout to settle before measuring
requestAnimationFrame(function() { requestAnimationFrame(resize); });
```

Why double `requestAnimationFrame`: the first frame is scheduled before layout is complete; the second is guaranteed after.

---

## Scale correction for pointer input

When the canvas CSS size differs from its internal resolution (always the case with `width: 100%`), correct coordinates:

```js
canvas.addEventListener('mousemove', function(e) {
  var rect = canvas.getBoundingClientRect();
  var scaleX = CW / rect.width;
  var scaleY = CH / rect.height;
  var x = (e.clientX - rect.left) * scaleX;
  var y = (e.clientY - rect.top)  * scaleY;
  // use x, y for game logic
});

canvas.addEventListener('touchmove', function(e) {
  e.preventDefault(); // must be in non-passive listener
  var t = e.touches[0];
  var rect = canvas.getBoundingClientRect();
  var x = (t.clientX - rect.left) * (CW / rect.width);
  var y = (t.clientY - rect.top)  * (CH / rect.height);
}, { passive: false });
```

**Always pass `{ passive: false }` for touchmove/touchstart handlers that call `e.preventDefault()`.**

---

## Game loop: rAF vs setInterval

| Pattern | Use when |
|---|---|
| `requestAnimationFrame` | Smooth animation, projectiles, physics (Bubble Shooter) |
| `setInterval` | Discrete tick-based games (Snake, 2048) |

rAF loop pattern:
```js
var animId;
function step() {
  update();
  draw();
  animId = requestAnimationFrame(step);
}
function startLoop() { animId = requestAnimationFrame(step); }
function stopLoop()  { cancelAnimationFrame(animId); }
```

---

## D-pad (tick-based games with directional input)

Only for games where the player controls a direction (Snake, Pac-Man).
**Hide on desktop, show on touch devices:**

```css
.dpad { display: none; }
@media (pointer: coarse) { .dpad { display: flex; } }
```

### D-pad layout (sticky bottom)

The D-pad must stick to the bottom of the screen. The board/canvas centers in the space between the HUD (top) and D-pad (bottom):

```css
/* Arrow pad — sticks to bottom of #app flex container */
.arrow-pad {
  margin-top: auto;      /* pushes down to bottom */
  padding-top: 1.5rem;   /* breathing room above buttons */
  gap: 12px;             /* between directional buttons */
  display: grid;
  grid-template-areas:
    ".  up  ."
    "lt  .  rt"
    ". dn  .";
  justify-content: center;
}
```

### Board centering (between HUD and D-pad)

The game board should center vertically in the remaining space:

```css
.board-wrap {
  margin-top: auto;
  margin-bottom: auto;   /* centers in flex space between HUD and D-pad */
}
```

Layout order in `#app` (flex column): HUD → board-wrap (centered) → arrow-pad (sticky bottom).

### D-pad button sizing

```css
.dpad-btn {
  width: 64px; height: 64px;   /* 64px default, comfortable tap target */
  touch-action: manipulation;
  -webkit-tap-highlight-color: transparent;
  user-select: none;
}
@media (max-width: 480px) {
  .dpad-btn { width: 72px; height: 72px; }
}
```

Bind `touchstart` (not `touchend`) for instant response:
```js
btn.addEventListener('touchstart', function(e) {
  e.preventDefault();
  handleDirection(dx, dy);
}, { passive: false });
btn.addEventListener('mousedown', function() { handleDirection(dx, dy); });
```

---

## Swipe input (grid/board games)

For games like 2048 where the player swipes to move (no d-pad needed).
Attach swipe listeners **only on the board/canvas container** -- never on `document` or `body`, as that conflicts with the webview scroll.

```js
(function() {
  var el = document.getElementById('board-wrap'); // scoped to game area only
  var sx, sy;
  var THRESHOLD = 30; // px -- ignore accidental taps

  el.addEventListener('touchstart', function(e) {
    var t = e.touches[0];
    sx = t.clientX;
    sy = t.clientY;
  }, { passive: true });

  el.addEventListener('touchend', function(e) {
    var t = e.changedTouches[0];
    var dx = t.clientX - sx;
    var dy = t.clientY - sy;
    if (Math.abs(dx) < THRESHOLD && Math.abs(dy) < THRESHOLD) return; // tap, not swipe
    if (Math.abs(dx) > Math.abs(dy)) {
      handleDirection(dx > 0 ? 'right' : 'left');
    } else {
      handleDirection(dy > 0 ? 'down' : 'up');
    }
  });
})();
```

Rules:
- **Scope to the game container** (`#board-wrap`, `#canvas-wrap`) -- never `document`
- **Threshold of 30px** to filter accidental taps
- **`passive: true`** on `touchstart` (no `preventDefault` needed)
- Use `touchend` + `changedTouches[0]` to compute direction
- Combine with keyboard input for desktop (arrow keys / WASD)

---

## Keyboard hint

Show only on desktop (pointer: fine = mouse):
```html
<p class="hint" id="kb-hint">Arrow keys or WASD &nbsp;·&nbsp; Space to pause</p>
```
```css
.hint { display: none; font-size: 0.72rem; color: var(--text-dim); margin-top: 0.5rem; }
@media (pointer: fine) { .hint { display: block; } }
```

---

## HUD (score bar)

```css
#hud {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  max-width: 480px;
  padding: 6px 12px 4px;
  flex-shrink: 0; /* never shrink -- always visible above canvas */
}
```

---

## Overlay (start / pause / game over)

Consistent pattern across all games:

```html
<div id="overlay">
  <h2 id="ov-title">Game Name</h2>
  <p id="ov-msg">Tap to play</p>
  <button class="btn btn-primary btn-lg" id="play-btn" onclick="startGame()">Play</button>
</div>
```

```css
#overlay {
  position: absolute; inset: 0;
  display: flex; flex-direction: column;
  align-items: center; justify-content: center;
  background: color-mix(in srgb, var(--text) 62%, transparent);
}
#overlay h2 { font-size: 1.7rem; font-weight: 700; color: var(--bg); margin-bottom: 0.4rem; }
#overlay p  { color: rgba(255,255,255,0.7); font-size: 0.88rem; margin-bottom: 1.4rem; text-align: center; line-height: 1.5; }
/* Use alf-ui.css .btn and .btn-primary classes — no need to redefine .btn here.
   Only add game-specific overrides: */
#overlay .btn { font-size: 1rem; padding: 10px 28px; }
```

Show/hide:
```js
function showOverlay(title, msg, btnText) {
  document.getElementById('ov-title').textContent = title;
  document.getElementById('ov-msg').textContent   = msg;
  document.getElementById('play-btn').textContent = btnText;
  document.getElementById('overlay').classList.remove('hidden');
}
function hideOverlay() {
  document.getElementById('overlay').classList.add('hidden');
}
```

---

## High score persistence

Use `AlfSDK.storage` (server-side, survives app updates). `localStorage` is not available in sandboxed iframes.

```js
var highscore = 0;
AlfSDK.storage.get('highScore').then(function(val) {
  highscore = val || 0;
  document.getElementById('best').textContent = highscore;
});

function updateBest(s) {
  if (s > highscore) {
    highscore = s;
    AlfSDK.storage.set('highScore', s);
    document.getElementById('best').textContent = highscore;
  }
}
```

---

## Colors from theme

Always read CSS variables at runtime, never hardcode:

```js
var C = {};
function refreshColors() {
  var s = getComputedStyle(document.documentElement);
  function v(n) { return s.getPropertyValue(n).trim() || '#888'; }
  C.bg    = v('--bg-card');
  C.grid  = v('--border');
  C.snake = v('--accent');
  C.head  = v('--green');
  C.food  = v('--red');
}
```

Call `refreshColors()` inside `resize()` and inside `onThemeChange`.

Available palette vars: `--accent`, `--green`, `--red`, `--yellow`, `--mauve`, `--sapphire`, `--pink`, `--teal`, `--peach`, `--lavender`, `--bg`, `--bg-card`, `--bg-input`, `--border`, `--text`, `--text-dim`, `--on-accent`. Full reference in `AIG.md`.

---

## Checklist for games

- [ ] `viewport-fit=cover` in meta viewport
- [ ] `100dvh` on html/body + `overflow: hidden`
- [ ] `env(safe-area-inset-*)` padding on `#app`
- [ ] `resize()` function called on `window.addEventListener('resize')` and double rAF at init
- [ ] Canvas CSS: `width: 100%; height: 100%` (never fixed px)
- [ ] Canvas container: `flex: 1 1 auto; min-height: 0`
- [ ] Scale correction for all pointer/touch input
- [ ] `touch-action: none` on `#canvas-wrap`
- [ ] `{ passive: false }` on all `touchmove`/`touchstart` handlers that call `preventDefault`
- [ ] Swipe input scoped to game container (not `document`) with 30px threshold
- [ ] D-pad hidden on desktop via `@media (pointer: coarse)`
- [ ] Keyboard hint hidden on mobile via `@media (pointer: fine)`
- [ ] `refreshColors()` called in `resize()` and `onThemeChange`
- [ ] **Audio via `AlfSDK.audio`** — NEVER create your own AudioContext
- [ ] High scores via `AlfSDK.storage` (not localStorage)
- [ ] Game over dialog via `AlfSDK.confirm()` (not window.confirm)
- [ ] Overlay pattern: title + msg + btn
- [ ] No compiled binary -- REST server architecture, no CLI tool
