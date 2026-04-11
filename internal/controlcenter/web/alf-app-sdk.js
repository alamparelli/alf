/**
 * ALF App SDK v4.0.0
 * SDK for marketplace apps running in sandboxed iframes.
 * Communication via MessageChannel (no postMessage wildcard).
 * Auth via Bearer token (no cookies — iframe is sandboxed).
 *
 * Modules:
 *   Core      — init, api, bash, tool, navigate, toast, getTheme
 *   Audio     — AlfSDK.audio.load/play/playUrl (mobile autoplay unlock)
 *   Sheet     — AlfSDK.sheet/closeSheet (native CC bottom-sheet modals)
 *   Storage   — AlfSDK.storage.get/set/remove/clear (per-app key/value)
 *   Dialog    — AlfSDK.confirm/prompt (native CC dialog, bottom-sheet on mobile)
 *   Events    — AlfSDK.events.on/off/emit (inter-app pub/sub via parent)
 *   Viewport  — AlfSDK.viewport.isMobile/safeArea/orientation/onChange
 *   Haptics   — AlfSDK.haptics.tap/notify/vibrate
 *   Clipboard — AlfSDK.clipboard.write/read
 *   I18n      — AlfSDK.i18n.locale/lang/dir
 *   Badge     — AlfSDK.badge.set/increment/clear
 *
 * Usage:
 *   <script src="/static/alf-app-sdk.js"></script>
 *   AlfSDK.init({ slug: 'my-app', onThemeChange: fn });
 */
(function(global) {
  'use strict';

  var _msgId = 0;
  var _pendingReplies = {}; // id -> { resolve, reject }
  var _port = null; // MessagePort from parent handshake
  var _token = null; // Bearer token for authenticated API calls
  var _theme = { palette: 'sage', dark: false }; // theme from parent
  var _safeAreas = { top: '0px', bottom: '0px', left: '0px', right: '0px' };

  function postToParent(action, data) {
    if (!_port) return;
    _port.postMessage(Object.assign({ type: 'alf-app', action: action }, data || {}));
  }

  function requestFromParent(action, data) {
    return new Promise(function(resolve, reject) {
      var id = ++_msgId;
      _pendingReplies[id] = { resolve: resolve, reject: reject };
      postToParent(action, Object.assign({ _replyId: id }, data || {}));
      // Timeout after 30s
      setTimeout(function() {
        if (_pendingReplies[id]) {
          delete _pendingReplies[id];
          reject(new Error('Parent reply timeout'));
        }
      }, 30000);
    });
  }

  /** Handle messages from parent via MessagePort */
  function _handlePortMessage(e) {
    var msg = e.data;
    if (!msg || msg.type !== 'alf') return;

    // Initial context (sent right after handshake)
    if (msg.action === 'init-context') {
      if (msg.token) _token = msg.token;
      if (msg.theme) {
        _theme = msg.theme;
        if (SDK._listeners.theme) SDK._listeners.theme(msg.theme.palette, msg.theme.dark);
      }
      if (msg.safeAreas) {
        _safeAreas = msg.safeAreas;
        _applySafeAreas(msg.safeAreas);
      }
      if (msg.permissions !== undefined) {
        SDK._permissions = msg.permissions;
      }
      SDK._ready = true;
      if (SDK._readyResolve) SDK._readyResolve();
      return;
    }

    // Token refresh
    if (msg.action === 'token-refresh') {
      if (msg.token) _token = msg.token;
      return;
    }

    // Theme sync
    if (msg.action === 'theme') {
      _theme = { palette: msg.palette, dark: msg.dark };
      if (SDK._listeners.theme) SDK._listeners.theme(msg.palette, msg.dark);
      return;
    }

    // Destroy
    if (msg.action === 'destroy' && SDK._listeners.destroy) {
      SDK._listeners.destroy();
      return;
    }

    // Lifecycle visibility from parent
    if (msg.action === 'visible' && SDK._listeners.visible) {
      SDK._listeners.visible();
      return;
    }
    if (msg.action === 'hidden' && SDK._listeners.hidden) {
      SDK._listeners.hidden();
      return;
    }

    // Inter-app event relay
    if (msg.action === 'event-relay') {
      EventManager._dispatch(msg.event, msg.payload);
      return;
    }

    // Sheet action callback relay
    if (msg.action === 'sheet-action' && msg.name && SDK._sheetActions) {
      var handler = SDK._sheetActions[msg.name];
      if (handler) {
        try { handler(msg.params || {}); } catch(err) { console.warn('[AlfSDK] sheet action error:', err); }
      }
      return;
    }

    // Reply to requestFromParent
    if (msg.action === 'reply' && msg._replyId) {
      var pending = _pendingReplies[msg._replyId];
      if (pending) {
        delete _pendingReplies[msg._replyId];
        if (msg.error) pending.reject(new Error(msg.error));
        else pending.resolve(msg.result);
      }
      return;
    }
  }

  /** Apply safe area CSS variables to :root */
  function _applySafeAreas(areas) {
    var style = document.getElementById('alf-safe-areas');
    if (!style) {
      style = document.createElement('style');
      style.id = 'alf-safe-areas';
      document.head.appendChild(style);
    }
    style.textContent =
      ':root {\n' +
      '  --safe-area-top: ' + (areas.top || '0px') + ';\n' +
      '  --safe-area-bottom: ' + (areas.bottom || '0px') + ';\n' +
      '  --safe-area-left: ' + (areas.left || '0px') + ';\n' +
      '  --safe-area-right: ' + (areas.right || '0px') + ';\n' +
      '  --page-padding-top: 1rem;\n' +
      '}\n' +
      'body {\n' +
      '  padding: 0 var(--safe-area-right) var(--safe-area-bottom) var(--safe-area-left);\n' +
      '  overflow-x: hidden;\n' +
      '}';
  }

  // ── Audio Manager ──────────────────────────────────────────────────
  var AudioManager = {
    _ctx: null,
    _unlocked: false,
    _bufferCache: {},
    _pendingUnlock: [],

    _tryUnlock: function() {
      try {
        if (!this._ctx) this._ctx = new (window.AudioContext || window.webkitAudioContext)();
        var self = this;
        function markUnlocked() {
          if (!self._unlocked) {
            self._unlocked = true;
            self._flushPending();
          }
        }
        if (this._ctx.state === 'suspended') {
          this._ctx.resume();
          var check = setInterval(function() {
            if (self._ctx && self._ctx.state === 'running') {
              clearInterval(check);
              markUnlocked();
            }
          }, 50);
          this._ctx.resume().then(function() {
            clearInterval(check);
            markUnlocked();
          });
          setTimeout(function() { clearInterval(check); }, 5000);
        } else {
          markUnlocked();
        }
      } catch(e) { /* retry next gesture */ }
    },

    _flushPending: function() {
      while (this._pendingUnlock.length) (this._pendingUnlock.shift())();
    },

    _installListeners: function() {
      var self = this;
      var done = false;
      var handler = function() {
        self._tryUnlock();
        if (self._unlocked && !done) {
          done = true;
          ['click','touchstart','touchend','keydown'].forEach(function(evt) {
            document.removeEventListener(evt, handler);
          });
        }
      };
      ['click','touchstart','touchend','keydown'].forEach(function(evt) {
        document.addEventListener(evt, handler);
      });
    },

    getContext: function() { return this._ctx; },
    isUnlocked: function() { return this._unlocked; },

    load: function(url) {
      var self = this;
      if (this._bufferCache[url]) return Promise.resolve(this._bufferCache[url]);
      return fetch(url).then(function(r) {
        if (!r.ok) throw new Error('Audio load failed: ' + r.status);
        return r.arrayBuffer();
      }).then(function(data) {
        if (!self._ctx) throw new Error('AudioContext not ready');
        return self._ctx.decodeAudioData(data);
      }).then(function(buffer) {
        self._bufferCache[url] = buffer;
        return buffer;
      });
    },

    play: function(buffer, opts) {
      if (!this._ctx || !this._unlocked) return null;
      opts = opts || {};
      if (this._ctx.state === 'suspended') this._ctx.resume();
      var src = this._ctx.createBufferSource();
      src.buffer = buffer;
      src.loop = !!opts.loop;
      var gain = this._ctx.createGain();
      gain.gain.value = opts.volume !== undefined ? opts.volume : 1.0;
      src.connect(gain);
      gain.connect(this._ctx.destination);
      src.start();
      return src;
    },

    playUrl: function(url, opts) {
      var self = this;
      return this.load(url).then(function(buf) { return self.play(buf, opts); });
    },

    onUnlock: function(cb) {
      if (this._unlocked) { cb(); return; }
      this._pendingUnlock.push(cb);
    }
  };

  // ── Storage ────────────────────────────────────────────────────────
  var StorageManager = {
    _slug: null,

    _path: function(key) {
      var base = '/api/apps/' + this._slug + '/storage';
      return key ? base + '?key=' + encodeURIComponent(key) : base;
    },

    get: function(key) {
      return SDK.api(this._path(key)).then(function(data) {
        return key ? data.value : data;
      }).catch(function() {
        return key ? null : {};
      });
    },

    set: function(keyOrObj, value) {
      var payload = {};
      if (typeof keyOrObj === 'string') {
        payload[keyOrObj] = value;
      } else {
        payload = keyOrObj;
      }
      return SDK.api(this._path(), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
    },

    remove: function(key) {
      return SDK.api(this._path(key), { method: 'DELETE' });
    },

    clear: function() {
      return SDK.api(this._path(), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({})
      });
    },

    keys: function() {
      return SDK.api(this._path() + '?list=keys').then(function(d) { return d.keys; });
    },

    entries: function() {
      return SDK.api(this._path() + '?list=entries').then(function(d) { return d.entries; });
    }
  };

  // ── Events (inter-app pub/sub, auto-namespaced by slug) ────────────
  var EventManager = {
    _handlers: {},

    _resolveEvent: function(event) {
      if (event.indexOf(':') >= 0) return event;
      return SDK._slug ? SDK._slug + ':' + event : event;
    },

    on: function(event, handler) {
      var resolved = this._resolveEvent(event);
      if (!this._handlers[resolved]) this._handlers[resolved] = [];
      this._handlers[resolved].push(handler);
    },

    off: function(event, handler) {
      var resolved = this._resolveEvent(event);
      if (!this._handlers[resolved]) return;
      this._handlers[resolved] = this._handlers[resolved].filter(function(h) { return h !== handler; });
    },

    emit: function(event, data) {
      var namespaced = SDK._slug ? SDK._slug + ':' + event : event;
      postToParent('event-emit', { event: namespaced, payload: data });
    },

    _dispatch: function(event, data) {
      var list = this._handlers[event] || [];
      for (var i = 0; i < list.length; i++) {
        try { list[i](data); } catch(e) { console.warn('[AlfSDK] event handler error:', e); }
      }
    }
  };

  // ── Viewport ───────────────────────────────────────────────────────
  var ViewportManager = {
    _changeListeners: [],

    isMobile: function() {
      return window.innerWidth <= 768;
    },

    isPWA: function() {
      return window.matchMedia('(display-mode: standalone)').matches ||
             window.navigator.standalone === true;
    },

    safeArea: function() {
      return {
        top: parseFloat(_safeAreas.top) || 0,
        bottom: parseFloat(_safeAreas.bottom) || 0,
        left: parseFloat(_safeAreas.left) || 0,
        right: parseFloat(_safeAreas.right) || 0
      };
    },

    orientation: function() {
      if (screen.orientation) return screen.orientation.type.indexOf('portrait') >= 0 ? 'portrait' : 'landscape';
      return window.innerHeight > window.innerWidth ? 'portrait' : 'landscape';
    },

    size: function() {
      return { width: window.innerWidth, height: window.innerHeight };
    },

    onChange: function(cb) {
      this._changeListeners.push(cb);
    },

    _notify: function() {
      var info = { mobile: this.isMobile(), orientation: this.orientation(), size: this.size() };
      for (var i = 0; i < this._changeListeners.length; i++) {
        try { this._changeListeners[i](info); } catch(e) {}
      }
    },

    _install: function() {
      var self = this;
      window.addEventListener('resize', function() { self._notify(); });
      if (screen.orientation) {
        screen.orientation.addEventListener('change', function() { self._notify(); });
      }
    }
  };

  // ── Haptics ────────────────────────────────────────────────────────
  var HapticsManager = {
    _available: typeof navigator !== 'undefined' && 'vibrate' in navigator,

    tap: function() {
      if (this._available) navigator.vibrate(10);
    },
    notify: function() {
      if (this._available) navigator.vibrate([30, 50, 30]);
    },
    success: function() {
      if (this._available) navigator.vibrate([10, 30, 20, 30, 40]);
    },
    error: function() {
      if (this._available) navigator.vibrate([50, 50, 100]);
    },
    vibrate: function(pattern) {
      if (this._available) navigator.vibrate(pattern);
    },
    isAvailable: function() {
      return this._available;
    }
  };

  // ── Clipboard ──────────────────────────────────────────────────────
  var ClipboardManager = {
    write: function(text) {
      if (!_hasPerm('clipboard')) return Promise.reject(new Error('Permission denied: clipboard'));
      return requestFromParent('clipboard-write', { text: text });
    },
    read: function() {
      if (!_hasPerm('clipboard')) return Promise.reject(new Error('Permission denied: clipboard'));
      return requestFromParent('clipboard-read');
    }
  };

  // ── I18n ───────────────────────────────────────────────────────────
  var I18nManager = {
    locale: function() {
      return navigator.language || navigator.userLanguage || 'en';
    },
    lang: function() {
      return this.locale().split('-')[0];
    },
    dir: function() {
      var rtlLangs = ['ar','he','fa','ur','ps','yi','sd'];
      return rtlLangs.indexOf(this.lang()) >= 0 ? 'rtl' : 'ltr';
    },
    languages: function() {
      return navigator.languages ? Array.from(navigator.languages) : [this.locale()];
    }
  };

  // ── Badge ──────────────────────────────────────────────────────────
  var BadgeManager = {
    _slug: null,

    set: function(count) {
      postToParent('badge-set', { slug: this._slug, count: count });
    },
    increment: function() {
      postToParent('badge-increment', { slug: this._slug });
    },
    clear: function() {
      postToParent('badge-set', { slug: this._slug, count: 0 });
    }
  };

  // ── Main SDK ───────────────────────────────────────────────────────
  var SDK = {
    VERSION: '4.0.0',
    _ready: false,
    _readyPromise: null,
    _readyResolve: null,
    _slug: null,
    _listeners: {},
    _authFailed: false,
    _permissions: null,

    audio: AudioManager,
    storage: StorageManager,
    events: EventManager,
    viewport: ViewportManager,
    haptics: HapticsManager,
    clipboard: ClipboardManager,
    i18n: I18nManager,
    badge: BadgeManager,

    /**
     * Initialize the SDK. Call once on page load.
     * @param {Object} opts
     * @param {string} opts.slug - App slug
     * @param {function} [opts.onThemeChange] - Called with (palette, isDark)
     * @param {function} [opts.onDestroy] - Called when app is unloaded
     * @param {function} [opts.onVisible] - Called when app becomes visible
     * @param {function} [opts.onHidden] - Called when app becomes hidden
     */
    init: function(opts) {
      opts = opts || {};
      this._slug = opts.slug || '';
      var self2 = this;
      this._readyPromise = new Promise(function(resolve) { self2._readyResolve = resolve; });

      // Bind slug to sub-managers
      StorageManager._slug = this._slug;
      BadgeManager._slug = this._slug;

      // Install subsystem listeners
      AudioManager._installListeners();
      ViewportManager._install();

      if (opts.onThemeChange) this._listeners.theme = opts.onThemeChange;
      if (opts.onDestroy) this._listeners.destroy = opts.onDestroy;
      if (opts.onVisible) this._listeners.visible = opts.onVisible;
      if (opts.onHidden) this._listeners.hidden = opts.onHidden;

      // Wait for MessageChannel handshake from parent
      window.addEventListener('message', function(e) {
        if (!e.data || e.data.type !== 'alf-handshake' || !e.ports || !e.ports[0]) return;
        _port = e.ports[0];
        _port.onmessage = _handlePortMessage;
        // Notify parent that app is ready
        postToParent('ready', { slug: SDK._slug });
      }, { once: true });

      // Lifecycle: Page Visibility API
      var self = this;
      document.addEventListener('visibilitychange', function() {
        if (document.hidden) {
          if (self._listeners.hidden) self._listeners.hidden();
        } else {
          if (self._listeners.visible) self._listeners.visible();
        }
      });

      // Auto-capture and report errors
      _setupErrorReporting();
    },

    /**
     * Authenticated API call using Bearer token.
     * @param {string} path - API path
     * @param {Object} [opts] - fetch options
     * @returns {Promise<any>}
     */
    api: function(path, opts) {
      if (!this._readyPromise) return Promise.reject(new Error('SDK not initialized — call AlfSDK.init() first'));
      if (this._authFailed) return Promise.reject(new Error('Session expired — reload page'));
      return this._readyPromise.then(function() {
        opts = opts || {};
        opts.headers = opts.headers || {};
        opts.headers['X-Requested-With'] = 'XMLHttpRequest';
        if (_token) {
          opts.headers['Authorization'] = 'Bearer ' + _token;
        }
        return fetch(path, opts);
      }).then(function(r) {
        if (r.status === 401) {
          SDK._authFailed = true;
          SDK.toast('Session expired — reload page to reconnect', 'error');
          throw new Error('401 Unauthorized');
        }
        if (!r.ok) {
          return r.json().then(function(j) { throw j; }).catch(function() {
            throw new Error(r.statusText);
          });
        }
        var ct = r.headers.get('content-type') || '';
        return ct.indexOf('json') >= 0 ? r.json() : r.text();
      });
    },

    /**
     * Raw authenticated fetch — returns the Response object (not parsed).
     * Use for binary downloads, streaming, or when you need response headers.
     *
     * NOTE: For <img>, <audio>, <video>, and @font-face, you do NOT need
     * AlfSDK.fetch(). If your app backend serves the asset under /api/ with
     * a media/font file extension, you can use the URL directly:
     *
     *   <img src="/apps/SLUG/api/covers/42.jpg">
     *
     * Allowed bare-URL extensions under /api/:
     *   .png .jpg .jpeg .gif .webp .svg .ico .avif
     *   .woff .woff2 .ttf .otf .eot
     *   .mp3 .mp4 .webm .ogg .wav
     *
     * Anything else under /api/ (.json, .js, .css, .wasm, .html, ...) still
     * requires AlfSDK.api() / AlfSDK.fetch() so the Bearer token is attached.
     *
     * @param {string} path - URL path
     * @param {Object} [opts] - fetch options
     * @returns {Promise<Response>}
     */
    fetch: function(path, opts) {
      if (!this._readyPromise) return Promise.reject(new Error('SDK not initialized — call AlfSDK.init() first'));
      return this._readyPromise.then(function() {
        opts = opts || {};
        opts.headers = opts.headers || {};
        if (_token) {
          opts.headers['Authorization'] = 'Bearer ' + _token;
        }
        return fetch(path, opts);
      });
    },

    /**
     * Execute a shell command via /api/bash.
     * @param {string} cmd
     * @returns {Promise<{output: string, exit_code: number, error: string}>}
     */
    bash: function(cmd) {
      if (!this._readyPromise) return Promise.reject(new Error('SDK not initialized — call AlfSDK.init() first'));
      return this.api('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd, app_slug: this._slug })
      });
    },

    /**
     * Run the app's CLI tool.
     * @param {string} action
     * @param {Object} [args]
     * @returns {Promise<string>}
     */
    tool: function(action, args) {
      if (!this._readyPromise) return Promise.reject(new Error('SDK not initialized — call AlfSDK.init() first'));
      var slug = this._slug;
      var bin = '/home/alf/data/tools/' + slug;
      var data = '/home/alf/data/apps/' + slug + '/data';
      var payload = Object.assign({ action: action }, args || {});
      var input = JSON.stringify(payload).replace(/'/g, "'\\''");
      var cmd = "echo '" + input + "' | ALF_APP_DATA_DIR=" + data + " " + bin;
      return this.bash(cmd).then(function(res) {
        if (res.exit_code !== 0) throw new Error(res.error || res.output || 'Command failed');
        return res.output || '';
      });
    },

    /**
     * Call a cross-app action.
     * @param {string} targetSlug
     * @param {string} actionName
     * @param {Object} [params]
     * @returns {Promise<any>}
     */
    action: function(targetSlug, actionName, params) {
      if (!this._readyPromise) return Promise.reject(new Error('SDK not initialized — call AlfSDK.init() first'));
      return this.api('/api/app-action', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target: targetSlug, action: actionName, params: params || {} })
      });
    },

    /** Navigate the parent CC to a view. */
    navigate: function(view) {
      if (!_ensureReady('navigate')) return;
      postToParent('navigate', { view: view });
    },

    /** Show a toast in the parent CC. */
    toast: function(msg, type) {
      if (!_ensureReady('toast')) return;
      postToParent('toast', { msg: msg, type: type || 'success' });
    },

    /** Get current theme (from parent handshake). */
    getTheme: function() {
      return { palette: _theme.palette, dark: _theme.dark };
    },

    /**
     * Show HTML in a native bottom sheet (mobile) / modal (desktop).
     * @param {string} html
     * @param {Object} [actions]
     */
    sheet: function(html, actions) {
      SDK._sheetActions = actions || {};
      postToParent('sheet', { html: html, hasActions: !!actions });
    },

    updateSheet: function(html) {
      postToParent('update-sheet', { html: html });
    },

    closeSheet: function() {
      SDK._sheetActions = {};
      postToParent('close-sheet');
    },

    /**
     * Show a confirmation dialog.
     * @param {string} message
     * @param {Object} [opts]
     * @returns {Promise<boolean>}
     */
    confirm: function(message, opts) {
      return requestFromParent('confirm', Object.assign({ message: message }, opts || {}));
    },

    /**
     * Show a prompt dialog.
     * @param {string} message
     * @param {Object} [opts]
     * @returns {Promise<string|null>}
     */
    prompt: function(message, opts) {
      return requestFromParent('prompt', Object.assign({ message: message }, opts || {}));
    },

    /**
     * Upload a file to the app's storage.
     * @param {File} file
     * @returns {Promise<{path: string, name: string, size: number}>}
     */
    upload: function(file) {
      if (!_ensureReady('upload')) return Promise.reject(new Error('SDK not initialized'));
      var formData = new FormData();
      formData.append('file', file);
      return SDK.api('/api/apps/' + SDK._slug + '/upload', {
        method: 'POST',
        body: formData
      });
    }
  };

  // ── Error reporting ───────────────────────────────────────────────
  function _setupErrorReporting() {
    function reportError(message, stack, source) {
      if (!SDK._ready || !SDK._slug) return;
      try {
        var body = JSON.stringify({
          message: String(message).slice(0, 1000),
          stack: String(stack || '').slice(0, 4000),
          source: source || 'unknown'
        });
        // Use fetch with Bearer token since sendBeacon can't set headers
        fetch('/api/apps/' + SDK._slug + '/errors', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Authorization': _token ? 'Bearer ' + _token : ''
          },
          body: body
        }).catch(function() {});
      } catch(e) { /* swallow */ }
    }

    window.addEventListener('error', function(e) {
      reportError(
        e.message || 'Unknown error',
        e.error && e.error.stack ? e.error.stack : (e.filename + ':' + e.lineno),
        'onerror'
      );
    });

    window.addEventListener('unhandledrejection', function(e) {
      var msg = 'Unhandled promise rejection';
      var stack = '';
      if (e.reason) {
        msg = e.reason.message || String(e.reason);
        stack = e.reason.stack || '';
      }
      reportError(msg, stack, 'unhandledrejection');
    });
  }

  function _ensureReady(method) {
    if (!SDK._ready) {
      console.warn('[AlfSDK] ' + method + '() called before init(). Call AlfSDK.init() first.');
      return false;
    }
    return true;
  }

  function _hasPerm(perm) {
    if (SDK._permissions === null || SDK._permissions === undefined) return true;
    return SDK._permissions.indexOf(perm) >= 0;
  }

  global.AlfSDK = SDK;
})(window);
