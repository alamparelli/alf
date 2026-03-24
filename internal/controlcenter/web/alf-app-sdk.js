/**
 * ALF App SDK v1.0
 * Lightweight SDK for marketplace apps running in iframes.
 * Provides theme sync, authenticated API calls, CLI tool invocation,
 * parent navigation, and toast notifications.
 *
 * Usage:
 *   <script src="/static/alf-app-sdk.js"></script>
 *   AlfSDK.init({ slug: 'my-app', onThemeChange: function(palette, dark) { ... } });
 *   AlfSDK.tool('list').then(function(output) { ... });
 */
(function(global) {
  'use strict';

  var SDK = {
    _ready: false,
    _slug: null,
    _listeners: {},

    /**
     * Initialize the SDK. Call once on page load.
     * @param {Object} opts
     * @param {string} opts.slug - App slug (used for tool/data path resolution)
     * @param {function} [opts.onThemeChange] - Called with (palette, isDark) when theme changes
     * @param {function} [opts.onDestroy] - Called when app is being unloaded
     */
    init: function(opts) {
      opts = opts || {};
      this._slug = opts.slug || '';
      this._ready = true;

      if (opts.onThemeChange) this._listeners.theme = opts.onThemeChange;
      if (opts.onDestroy) this._listeners.destroy = opts.onDestroy;

      var self = this;
      window.addEventListener('message', function(e) {
        if (e.origin !== location.origin) return;
        var msg = e.data;
        if (!msg || msg.type !== 'alf') return;

        if (msg.action === 'theme' && self._listeners.theme) {
          self._listeners.theme(msg.palette, msg.dark);
        }
        if (msg.action === 'destroy' && self._listeners.destroy) {
          self._listeners.destroy();
        }
      });

      // Notify parent that app is ready, request current theme
      if (parent !== window) {
        parent.postMessage({ type: 'alf-app', action: 'ready', slug: this._slug }, '*');
      }
    },

    /**
     * Authenticated API call. Same-origin cookies are used automatically.
     * @param {string} path - API path (e.g. '/api/vault/secrets')
     * @param {Object} [opts] - fetch options
     * @returns {Promise<any>}
     */
    api: function(path, opts) {
      opts = opts || {};
      opts.headers = opts.headers || {};
      opts.headers['X-Requested-With'] = 'XMLHttpRequest';
      opts.credentials = 'same-origin';
      return fetch(path, opts).then(function(r) {
        if (r.status === 401) {
          SDK.toast('Session expired', 'error');
          throw new Error('401 Unauthorized');
        }
        if (!r.ok) {
          return r.json().then(function(j) { throw j; }).catch(function() {
            throw new Error(r.statusText);
          });
        }
        return r.json();
      });
    },

    /**
     * Execute a shell command via /api/bash.
     * @param {string} cmd - Shell command to execute
     * @returns {Promise<{output: string, exit_code: number, error: string}>}
     */
    bash: function(cmd) {
      return this.api('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: cmd })
      });
    },

    /**
     * Run the app's CLI tool with a given action and arguments.
     * @param {string} action - Tool action name
     * @param {Object} [args] - Additional arguments
     * @returns {Promise<string>} - Tool output text
     */
    tool: function(action, args) {
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
     * Request the parent SPA to navigate to a view.
     * @param {string} view - View name (e.g. 'chat', 'settings', 'page:my-app')
     */
    navigate: function(view) {
      if (parent !== window) {
        parent.postMessage({ type: 'alf-app', action: 'navigate', view: view }, '*');
      }
    },

    /**
     * Show a toast notification in the parent SPA.
     * @param {string} msg - Toast message
     * @param {string} [type='success'] - Toast type: 'success', 'error', 'info'
     */
    toast: function(msg, type) {
      if (parent !== window) {
        parent.postMessage({ type: 'alf-app', action: 'toast', msg: msg, type: type || 'success' }, '*');
      }
    },

    /**
     * Get current theme information.
     * @returns {{ palette: string, dark: boolean }}
     */
    getTheme: function() {
      return {
        palette: localStorage.getItem('alf-palette') || 'sage',
        dark: window.matchMedia('(prefers-color-scheme: dark)').matches
      };
    }
  };

  global.AlfSDK = SDK;
})(window);
