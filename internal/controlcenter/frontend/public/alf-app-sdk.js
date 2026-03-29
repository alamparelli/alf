/**
 * ALF App SDK v3.0.0
 * Complete SDK for marketplace apps running in iframes.
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

  function postToParent(action, data) {
    if (parent === window) return;
    parent.postMessage(Object.assign({ type: 'alf-app', action: action }, data || {}), '*');
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
          // Fallback: poll state — resume() promise can hang on iOS webviews
          var check = setInterval(function() {
            if (self._ctx && self._ctx.state === 'running') {
              clearInterval(check);
              markUnlocked();
            }
          }, 50);
          // Also keep the promise path
          this._ctx.resume().then(function() {
            clearInterval(check);
            markUnlocked();
          });
          // Safety: stop polling after 5s to avoid leaks
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

    /** Get all keys or a single key. */
    get: function(key) {
      return SDK.api(this._path(key)).then(function(data) {
        return key ? data.value : data;
      });
    },

    /** Set one or more keys (pass object). */
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

    /** Remove a key. */
    remove: function(key) {
      return SDK.api(this._path(key), { method: 'DELETE' });
    },

    /** Clear all storage for this app. */
    clear: function() {
      return SDK.api(this._path(), {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({})
      });
    },

    /** List all keys. */
    keys: function() {
      return SDK.api(this._path() + '?list=keys').then(function(d) { return d.keys; });
    },

    /** List all entries as [{key, value}, ...]. */
    entries: function() {
      return SDK.api(this._path() + '?list=entries').then(function(d) { return d.entries; });
    }
  };

  // ── Events (inter-app pub/sub, auto-namespaced by slug) ────────────
  var EventManager = {
    _handlers: {},

    /** Resolve event name: prefix with slug unless already namespaced (contains ':'). */
    _resolveEvent: function(event) {
      if (event.indexOf(':') >= 0) return event;
      return SDK._slug ? SDK._slug + ':' + event : event;
    },

    /** Subscribe to a custom event. Use 'slug:event' for cross-app, 'event' for self. */
    on: function(event, handler) {
      var resolved = this._resolveEvent(event);
      if (!this._handlers[resolved]) this._handlers[resolved] = [];
      this._handlers[resolved].push(handler);
    },

    /** Unsubscribe. */
    off: function(event, handler) {
      var resolved = this._resolveEvent(event);
      if (!this._handlers[resolved]) return;
      this._handlers[resolved] = this._handlers[resolved].filter(function(h) { return h !== handler; });
    },

    /** Emit an event (auto-prefixed with slug, relayed via parent to other apps). */
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

    /** True if viewport width <= 768px. */
    isMobile: function() {
      return window.innerWidth <= 768;
    },

    /** True if running as installed PWA (standalone). */
    isPWA: function() {
      return window.matchMedia('(display-mode: standalone)').matches ||
             window.navigator.standalone === true;
    },

    /** Safe area insets (CSS env values, parsed to numbers). */
    safeArea: function() {
      var style = getComputedStyle(document.documentElement);
      function px(prop) {
        var val = style.getPropertyValue('env(' + prop + ', 0px)');
        return parseFloat(val) || 0;
      }
      // Alternative: read from CSS custom properties if set
      return {
        top: px('safe-area-inset-top'),
        bottom: px('safe-area-inset-bottom'),
        left: px('safe-area-inset-left'),
        right: px('safe-area-inset-right')
      };
    },

    /** Current orientation: 'portrait' or 'landscape'. */
    orientation: function() {
      if (screen.orientation) return screen.orientation.type.indexOf('portrait') >= 0 ? 'portrait' : 'landscape';
      return window.innerHeight > window.innerWidth ? 'portrait' : 'landscape';
    },

    /** Screen dimensions. */
    size: function() {
      return { width: window.innerWidth, height: window.innerHeight };
    },

    /** Register a callback for viewport changes (resize, orientation). */
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

    /** Light tap feedback (10ms). */
    tap: function() {
      if (this._available) navigator.vibrate(10);
    },

    /** Notification feedback (double pulse). */
    notify: function() {
      if (this._available) navigator.vibrate([30, 50, 30]);
    },

    /** Success feedback (rising pattern). */
    success: function() {
      if (this._available) navigator.vibrate([10, 30, 20, 30, 40]);
    },

    /** Error feedback (heavy buzz). */
    error: function() {
      if (this._available) navigator.vibrate([50, 50, 100]);
    },

    /** Custom vibration pattern. */
    vibrate: function(pattern) {
      if (this._available) navigator.vibrate(pattern);
    },

    /** True if device supports vibration. */
    isAvailable: function() {
      return this._available;
    }
  };

  // ── Clipboard ──────────────────────────────────────────────────────
  var ClipboardManager = {
    /** Copy text to clipboard (delegates to parent for cross-frame support). */
    write: function(text) {
      if (!_hasPerm('clipboard')) return Promise.reject(new Error('Permission denied: clipboard'));
      return requestFromParent('clipboard-write', { text: text });
    },

    /** Read text from clipboard (delegates to parent). */
    read: function() {
      if (!_hasPerm('clipboard')) return Promise.reject(new Error('Permission denied: clipboard'));
      return requestFromParent('clipboard-read');
    }
  };

  // ── I18n ───────────────────────────────────────────────────────────
  var I18nManager = {
    /** Full locale string (e.g. 'en-US', 'fr-FR'). */
    locale: function() {
      return navigator.language || navigator.userLanguage || 'en';
    },

    /** Language code (e.g. 'en', 'fr'). */
    lang: function() {
      return this.locale().split('-')[0];
    },

    /** Text direction: 'ltr' or 'rtl'. */
    dir: function() {
      var rtlLangs = ['ar','he','fa','ur','ps','yi','sd'];
      return rtlLangs.indexOf(this.lang()) >= 0 ? 'rtl' : 'ltr';
    },

    /** All preferred languages (navigator.languages). */
    languages: function() {
      return navigator.languages ? Array.from(navigator.languages) : [this.locale()];
    }
  };

  // ── Badge ──────────────────────────────────────────────────────────
  var BadgeManager = {
    _slug: null,

    /** Set badge count on the app's sidebar icon. */
    set: function(count) {
      postToParent('badge-set', { slug: this._slug, count: count });
    },

    /** Increment badge by 1. */
    increment: function() {
      postToParent('badge-increment', { slug: this._slug });
    },

    /** Clear the badge. */
    clear: function() {
      postToParent('badge-set', { slug: this._slug, count: 0 });
    }
  };

  // ── Main SDK ───────────────────────────────────────────────────────
  var SDK = {
    VERSION: '3.0.0',
    _ready: false,
    _slug: null,
    _listeners: {},
    _authFailed: false,
    _permissions: null, // null = all allowed, string[] = restricted set

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
     * @param {function} [opts.onVisible] - Called when app becomes visible (tab switch or browser focus)
     * @param {function} [opts.onHidden] - Called when app becomes hidden (tab switch or browser blur)
     */
    init: function(opts) {
      opts = opts || {};
      this._slug = opts.slug || '';
      this._ready = true;

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

      var self = this;
      window.addEventListener('message', function(e) {
        if (e.origin !== location.origin) return;
        var msg = e.data;
        if (!msg || msg.type !== 'alf') return;

        // Theme sync
        if (msg.action === 'theme' && self._listeners.theme) {
          self._listeners.theme(msg.palette, msg.dark);
        }
        // Destroy
        if (msg.action === 'destroy' && self._listeners.destroy) {
          self._listeners.destroy();
        }
        // Lifecycle visibility from parent
        if (msg.action === 'visible' && self._listeners.visible) {
          self._listeners.visible();
        }
        if (msg.action === 'hidden' && self._listeners.hidden) {
          self._listeners.hidden();
        }
        // Inter-app event relay
        if (msg.action === 'event-relay') {
          EventManager._dispatch(msg.event, msg.payload);
        }
        // Sheet action callback relay
        if (msg.action === 'sheet-action' && msg.name && SDK._sheetActions) {
          var handler = SDK._sheetActions[msg.name];
          if (handler) {
            try { handler(msg.params || {}); } catch(e) { console.warn('[AlfSDK] sheet action error:', e); }
          }
        }
        // Permissions received from parent
        if (msg.action === 'permissions') {
          SDK._permissions = msg.permissions; // null = all allowed, array = restricted
        }
        // Reply to requestFromParent
        if (msg.action === 'reply' && msg._replyId) {
          var pending = _pendingReplies[msg._replyId];
          if (pending) {
            delete _pendingReplies[msg._replyId];
            if (msg.error) pending.reject(new Error(msg.error));
            else pending.resolve(msg.result);
          }
        }
      });

      // Lifecycle: Page Visibility API
      document.addEventListener('visibilitychange', function() {
        if (document.hidden) {
          if (self._listeners.hidden) self._listeners.hidden();
        } else {
          if (self._listeners.visible) self._listeners.visible();
        }
      });

      // Auto-capture and report errors
      _setupErrorReporting();

      // Notify parent that app is ready
      postToParent('ready', { slug: this._slug });
    },

    /**
     * Authenticated API call.
     * @param {string} path - API path
     * @param {Object} [opts] - fetch options
     * @returns {Promise<any>}
     */
    api: function(path, opts) {
      if (!_ensureReady('api')) return Promise.reject(new Error('SDK not initialized'));
      if (this._authFailed) return Promise.reject(new Error('Session expired — reload page'));
      opts = opts || {};
      opts.headers = opts.headers || {};
      opts.headers['X-Requested-With'] = 'XMLHttpRequest';
      opts.credentials = 'same-origin';
      return fetch(path, opts).then(function(r) {
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
     * Execute a shell command via /api/bash.
     * @param {string} cmd
     * @returns {Promise<{output: string, exit_code: number, error: string}>}
     */
    bash: function(cmd) {
      if (!_ensureReady('bash')) return Promise.reject(new Error('SDK not initialized'));
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
      if (!_ensureReady('tool')) return Promise.reject(new Error('SDK not initialized'));
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

    /** Get current theme. */
    getTheme: function() {
      return {
        palette: localStorage.getItem('alf-palette') || 'sage',
        dark: window.matchMedia('(prefers-color-scheme: dark)').matches
      };
    },

    /**
     * Show HTML in a native bottom sheet (mobile) / modal (desktop).
     * Supports interactive callbacks via data-action attributes.
     *
     * @param {string} html - HTML content. Use data-action="name" on interactive elements.
     *   Additional data-* attributes are passed as params to the callback.
     * @param {Object} [actions] - Map of action name → callback function.
     *   Callback receives an object with all data-* attributes from the clicked element.
     *
     * @example
     *   AlfSDK.sheet(
     *     '<h3>Book Detail</h3>' +
     *     '<button data-action="delete" data-id="12">Delete</button>' +
     *     '<button data-action="rate" data-id="12" data-stars="5">★★★★★</button>',
     *     {
     *       delete: function(p) { deleteBook(p.id); AlfSDK.closeSheet(); },
     *       rate:   function(p) { setRating(p.id, p.stars); }
     *     }
     *   );
     */
    sheet: function(html, actions) {
      SDK._sheetActions = actions || {};
      postToParent('sheet', { html: html, hasActions: !!actions });
    },

    /**
     * Update the content of the currently open sheet without closing it.
     * Preserves scroll position. Actions map from the original sheet() call is kept.
     * @param {string} html - New HTML content
     */
    updateSheet: function(html) {
      postToParent('update-sheet', { html: html });
    },

    /** Close the current sheet. */
    closeSheet: function() {
      SDK._sheetActions = {};
      postToParent('close-sheet');
    },

    /**
     * Show a confirmation dialog (CC-native bottom sheet on mobile).
     * @param {string} message
     * @param {Object} [opts] - { title: string, confirmText: string, cancelText: string }
     * @returns {Promise<boolean>}
     */
    confirm: function(message, opts) {
      return requestFromParent('confirm', Object.assign({ message: message }, opts || {}));
    },

    /**
     * Show a prompt dialog (CC-native bottom sheet on mobile).
     * @param {string} message
     * @param {Object} [opts] - { title, defaultValue, placeholder, confirmText, multiline }
     * @param {boolean} [opts.multiline=false] - Render a textarea instead of a single-line input
     * @returns {Promise<string|null>} - Input value, or null if cancelled
     */
    prompt: function(message, opts) {
      return requestFromParent('prompt', Object.assign({ message: message }, opts || {}));
    },

    /**
     * Upload a file to the app's storage.
     * @param {File} file - File object (from input or drag-drop)
     * @returns {Promise<{path: string, name: string, size: number}>}
     */
    upload: function(file) {
      if (!_ensureReady('upload')) return Promise.reject(new Error('SDK not initialized'));
      var formData = new FormData();
      formData.append('file', file);
      return SDK.api('/api/apps/' + SDK._slug + '/upload', {
        method: 'POST',
        body: formData
        // Note: do NOT set Content-Type header — browser sets it with boundary
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
        navigator.sendBeacon(
          '/api/apps/' + SDK._slug + '/errors',
          new Blob([body], { type: 'application/json' })
        );
      } catch(e) { /* swallow — don't cause more errors */ }
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
    // null = no restrictions (backward compat / internal app)
    if (SDK._permissions === null || SDK._permissions === undefined) return true;
    return SDK._permissions.indexOf(perm) >= 0;
  }

  global.AlfSDK = SDK;
})(window);
