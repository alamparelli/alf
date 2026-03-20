// Auth: session cookie set by magic link flow (/auth endpoint).
// No tokens in URL, meta tags, or sessionStorage.
sessionStorage.removeItem('cc_token'); // cleanup legacy token

// CSRF: wrap native fetch to auto-inject X-Requested-With on same-origin state-changing requests.
const _nativeFetch = window.fetch;
window.fetch = function(url, opts) {
  opts = opts || {};
  const method = (opts.method || 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    opts.headers = opts.headers || {};
    if (!opts.headers['X-Requested-With']) opts.headers['X-Requested-With'] = 'XMLHttpRequest';
  }
  return _nativeFetch.call(this, url, opts);
};

function api(path, opts = {}) {
  const headers = { 'X-Requested-With': 'XMLHttpRequest', ...(opts.headers || {}) };
  return fetch(path, { ...opts, headers, credentials: 'same-origin' }).then(r => {
    if (r.status === 401) {
      toast('Session expired -send /login to your bot', 'error');
      throw new Error('401');
    }
    if (!r.ok && r.status !== 200) {
      return r.json().then(j => { throw j; });
    }
    return r.json();
  });
}

function toast(msg, type = 'success') {
  const el = document.getElementById('toast');
  el.textContent = msg;
  el.className = 'toast show ' + type;
  if (type === 'error') console.error('[toast]', msg, new Error().stack);
  setTimeout(() => el.className = 'toast', 3000);
}

// --- Restart: poll until daemon is back, then reload ---
function waitForDaemonAndReload(onStatus) {
  var dots = 0;
  var elapsed = 0;
  var interval = 1500;
  var maxWait = 120000;
  if (onStatus) onStatus('Restarting...');
  var timer = setInterval(function() {
    elapsed += interval;
    dots = (dots + 1) % 4;
    if (onStatus) onStatus('Waiting for daemon' + '.'.repeat(dots + 1) + ' (' + Math.round(elapsed / 1000) + 's)');
    if (elapsed >= maxWait) {
      clearInterval(timer);
      if (onStatus) onStatus('Daemon did not come back after ' + Math.round(maxWait / 1000) + 's. Try refreshing manually.');
      return;
    }
    fetch('/api/status', { credentials: 'same-origin' })
      .then(function(r) { if (r.ok) { clearInterval(timer); location.reload(); } })
      .catch(function() { /* still down, keep polling */ });
  }, interval);
}

// --- Desktop Notifications ---
// Request permission on first user interaction.
document.addEventListener('click', function reqNotif() {
  if ('Notification' in window && Notification.permission === 'default') {
    Notification.requestPermission();
  }
  document.removeEventListener('click', reqNotif);
}, { once: true });

function notify(title, body) {
  if (!document.hidden) return; // tab is focused, no need
  if (!('Notification' in window) || Notification.permission !== 'granted') return;
  const n = new Notification(title, {
    body: body ? body.substring(0, 200) : '',
    icon: '/static/icon.png',
    tag: 'alf-' + Date.now(),
  });
  n.onclick = () => { window.focus(); n.close(); };
  setTimeout(() => n.close(), 8000);
}

// --- Palette system (light/dark follows OS, per-file theme loading) ---
// --- Theme Factory ---
// Single registry: each entry defines an app palette + its matching terminal themes.
const ALF_THEMES = {
  sage:          { label: 'Sage',        light: '#f0f3ec', dark: '#222822', lightBorder: '#bcc5b8', darkBorder: '#3a4638', termLight: 'Sage Light',        termDark: 'Sage Dark' },
  studio:        { label: 'Studio',      light: '#f5f3f0', dark: '#1c1c1c', lightBorder: '#d6d3cf', darkBorder: '#333',    termLight: 'Studio Light',      termDark: 'Studio Dark' },
  catppuccin:    { label: 'Catppuccin',  light: '#eff1f5', dark: '#1e1e2e', lightBorder: '#ccd0da', darkBorder: '#45475a', termLight: 'Catppuccin Latte',  termDark: 'Catppuccin Mocha' },
  dracula:       { label: 'Dracula',     light: '#f8f8f2', dark: '#282a36', lightBorder: '#d8d8d0', darkBorder: '#44475a', termLight: 'Dracula',           termDark: 'Dracula' },
  solarized:     { label: 'Solarized',   light: '#fdf6e3', dark: '#002b36', lightBorder: '#d3cbb7', darkBorder: '#073642', termLight: 'Solarized Light',   termDark: 'Solarized Dark' },
  'tokyo-night': { label: 'Tokyo Night', light: '#d5d6db', dark: '#1a1b26', lightBorder: '#b8b9be', darkBorder: '#292e42', termLight: 'Tokyo Night',       termDark: 'Tokyo Night' },
  github:        { label: 'GitHub',      light: '#ffffff', dark: '#0d1117', lightBorder: '#d0d7de', darkBorder: '#30363d', termLight: 'GitHub Dark',       termDark: 'GitHub Dark' },
  nord:          { label: 'Nord',        light: '#eceff4', dark: '#2e3440', lightBorder: '#c8cdd5', darkBorder: '#434c5e', termLight: 'Nord',              termDark: 'Nord' },
};

function applyPalette(palette) {
  if (!palette || !ALF_THEMES[palette]) palette = 'sage';
  const link = document.getElementById('alf-theme-link');
  if (link) link.href = '/static/theme-' + palette + '.css';
  syncIframeTheme();
}

function syncTerminalTheme(palette) {
  const theme = ALF_THEMES[palette];
  if (!theme) return;
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  const termName = dark ? theme.termDark : theme.termLight;
  const sel = document.getElementById('termThemeSelect');
  if (sel && termName && typeof termThemes !== 'undefined' && termThemes[termName]) {
    sel.value = termName;
    if (typeof termInstance !== 'undefined' && termInstance) termInstance.options.theme = termThemes[termName];
  }
}

(function initPalette() {
  let saved = localStorage.getItem('alf-palette') ?? 'sage';
  if (!ALF_THEMES[saved]) saved = 'sage';
  applyPalette(saved);

  // Build theme picker dropdown from registry.
  const picker = document.getElementById('themePicker');
  if (picker) {
    Object.entries(ALF_THEMES).forEach(([key, t]) => {
      const opt = document.createElement('option');
      opt.value = key;
      opt.textContent = t.label;
      if (key === saved) opt.selected = true;
      picker.appendChild(opt);
    });
    picker.addEventListener('change', () => {
      const key = picker.value;
      localStorage.setItem('alf-palette', key);
      localStorage.removeItem('alf-term-theme');
      applyPalette(key);
      syncTerminalTheme(key);
    });
  }
})();

// --- Collapsible nav sections + favorites (persistent) ---
(function initNavSections() {
  const stored = JSON.parse(localStorage.getItem('alf-nav-collapsed') || '{}');
  const favs = JSON.parse(localStorage.getItem('alf-nav-favs') || '[]');

  // Add pin buttons to system nav items and restore favorites
  document.querySelectorAll('#navGrid .nav-item').forEach(item => {
    const view = item.dataset.view;
    if (favs.includes(view)) item.classList.add('nav-fav');
    const pin = document.createElement('button');
    pin.className = 'nav-fav-btn';
    pin.title = 'Pin to favorites';
    pin.innerHTML = '<i data-lucide="pin"></i>';
    pin.addEventListener('click', (e) => {
      e.stopPropagation();
      e.preventDefault();
      item.classList.toggle('nav-fav');
      const current = JSON.parse(localStorage.getItem('alf-nav-favs') || '[]');
      const v = item.dataset.view;
      const updated = item.classList.contains('nav-fav')
        ? [...current.filter(f => f !== v), v]
        : current.filter(f => f !== v);
      localStorage.setItem('alf-nav-favs', JSON.stringify(updated));
    });
    item.appendChild(pin);
  });

  document.querySelectorAll('.nav-section-toggle').forEach(btn => {
    const key = btn.dataset.section;
    const section = btn.closest('.nav-section');
    if (stored[key]) section.classList.add('collapsed');
    btn.addEventListener('click', () => {
      section.classList.toggle('collapsed');
      const state = JSON.parse(localStorage.getItem('alf-nav-collapsed') || '{}');
      state[key] = section.classList.contains('collapsed');
      localStorage.setItem('alf-nav-collapsed', JSON.stringify(state));
    });
  });
})();

// Inject theme CSS into iframe when it loads an app page.
function syncIframeTheme() {
  const frame = document.getElementById('pageFrame');
  try {
    const doc = frame.contentDocument;
    if (!doc || !doc.documentElement) return;
    const palette = localStorage.getItem('alf-palette') || 'sage';
    const themeHref = '/static/theme-' + palette + '.css';
    let link = doc.getElementById('alf-theme');
    if (!link) {
      link = doc.createElement('link');
      link.id = 'alf-theme';
      link.rel = 'stylesheet';
      doc.head.appendChild(link);
    }
    link.href = themeHref;
  } catch (_) { /* cross-origin or not loaded */ }
}
document.getElementById('pageFrame').addEventListener('load', syncIframeTheme);

// --- Sidebar navigation ---
const sidebar = document.getElementById('sidebar');
const sidebarOverlay = document.getElementById('sidebarOverlay');
const hamburgerBtn = document.getElementById('hamburgerBtn');

hamburgerBtn.addEventListener('click', () => {
  sidebar.classList.toggle('open');
  sidebarOverlay.classList.toggle('open');
});
sidebarOverlay.addEventListener('click', () => {
  sidebar.classList.remove('open');
  sidebarOverlay.classList.remove('open');
});

function navigateTo(view) {
  const homeView = document.getElementById('homeView');
  const chatView = document.getElementById('chatView');
  const schedulesView = document.getElementById('schedulesView');
  const tasksView = document.getElementById('tasksView');
  const teamsView = document.getElementById('teamsView');
  const pageFrame = document.getElementById('pageFrame');
  const docsView = document.getElementById('docsView');
  const logsView = document.getElementById('logsView');
  const tiersView = document.getElementById('tiersView');
  const firewallView = document.getElementById('firewallView');
  const vaultView = document.getElementById('vaultView');
  const terminalView = document.getElementById('terminalView');
  const settingsView = document.getElementById('settingsView');
  const marketplaceView = document.getElementById('marketplaceView');

  // Update active nav item -docs:id should highlight the docs nav item
  const navView = view.startsWith('docs:') ? 'docs' : (view.startsWith('page:') ? view : view);
  logsStopAutoRefresh();
  tasksStopAutoRefresh();
  fwStopAutoRefresh();
  document.querySelectorAll('#navGrid .nav-item, #navAppsSection .nav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.view === navView);
  });
  if (typeof syncBottomNav === 'function') syncBottomNav(navView);

  homeView.style.display = 'none';
  chatView.style.display = 'none';
  schedulesView.style.display = 'none';
  tasksView.style.display = 'none';
  if (teamsView) teamsView.style.display = 'none';
  pageFrame.style.display = 'none';
  docsView.style.display = 'none';
  logsView.style.display = 'none';
  tiersView.style.display = 'none';
  firewallView.style.display = 'none';
  vaultView.style.display = 'none';
  terminalView.style.display = 'none';
  settingsView.style.display = 'none';
  if (marketplaceView) marketplaceView.style.display = 'none';

  if (view === 'home') {
    homeView.style.display = '';
    pageFrame.src = '';
  } else if (view === 'chat') {
    chatView.style.display = '';
    pageFrame.src = '';
    chatClearBadge();
    chatLoadHistory();
    // Deferred scroll: the element was display:none so scrollHeight was 0
    // during any stream updates. Scroll to bottom now that it's visible.
    requestAnimationFrame(() => chatScrollBottom());
  } else if (view.startsWith('page:')) {
    const name = view.slice(5);
    pageFrame.style.display = '';
    pageFrame.src = '/apps/' + encodeURIComponent(name) + '/';
  } else if (view === 'schedules') {
    schedulesView.style.display = '';
    pageFrame.src = '';
    schedulesInit();
  } else if (view === 'tasks') {
    tasksView.style.display = '';
    pageFrame.src = '';
    tasksInit();
  } else if (view === 'teams') {
    if (teamsView) teamsView.style.display = '';
    pageFrame.src = '';
    teamsInit();
  } else if (view === 'logs') {
    logsView.style.display = '';
    pageFrame.src = '';
    logsInit();
  } else if (view === 'docs') {
    docsView.style.display = '';
    pageFrame.src = '';
    docsShowList();
  } else if (view.startsWith('docs:')) {
    docsView.style.display = '';
    pageFrame.src = '';
    docsShowArticle(view.slice(5));
  } else if (view === 'tiers') {
    tiersView.style.display = '';
    pageFrame.src = '';
    tiersInit();
  } else if (view === 'firewall') {
    firewallView.style.display = '';
    pageFrame.src = '';
    fwInit();
  } else if (view === 'vault') {
    vaultView.style.display = '';
    pageFrame.src = '';
    vaultInit();
  } else if (view === 'terminal') {
    terminalView.style.display = '';
    pageFrame.src = '';
    terminalInit();
  } else if (view === 'marketplace') {
    if (marketplaceView) marketplaceView.style.display = '';
    pageFrame.src = '';
    mpInit();
  } else if (view === 'settings') {
    settingsView.style.display = '';
    pageFrame.src = '';
  }

  localStorage.setItem('alf-view', view);

  // Close sidebar on mobile
  sidebar.classList.remove('open');
  sidebarOverlay.classList.remove('open');
}

// Bind system nav icons
document.querySelectorAll('#navGrid .nav-item').forEach(el => {
  el.addEventListener('click', () => navigateTo(el.dataset.view));
});

// Bind bottom nav bar (mobile).
document.querySelectorAll('#bottomNav .bottom-nav-item').forEach(el => {
  el.addEventListener('click', (e) => {
    e.preventDefault();
    const view = el.dataset.view;
    if (view === 'more') {
      // Open sidebar for access to all views.
      document.getElementById('sidebar').classList.add('open');
      document.getElementById('sidebarOverlay').classList.add('open');
    } else {
      navigateTo(view);
    }
  });
});

// Sidebar "more" link -> settings
const _moreLink = document.querySelector('.sidebar-more-link');
if (_moreLink) _moreLink.addEventListener('click', (e) => { e.preventDefault(); navigateTo('settings'); });

// Sync bottom nav active state with navigation.
function syncBottomNav(view) {
  document.querySelectorAll('#bottomNav .bottom-nav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.view === view);
  });
  // Sync chat badge.
  const chatBottomNav = document.querySelector('#bottomNav .bottom-nav-item[data-view="chat"]');
  const chatSideNav = document.querySelector('#navGrid .nav-item[data-view="chat"]');
  if (chatBottomNav && chatSideNav) {
    chatBottomNav.classList.toggle('has-badge', chatSideNav.classList.contains('has-badge'));
  }
}

// --- Status ---
let _updateDismissed = false;
function loadStatus() {
  api('/api/status').then(data => {
    if (data && data.version) {
      const ver = data.version.startsWith('v') ? data.version : 'v' + data.version;
      const el = document.getElementById('settingsVersion');
      if (el) el.textContent = ver;
    }
    if (data && data.update_available && !_updateDismissed) {
      showUpdateBanner(data.version, data.update_available);
    }
  }).catch(() => {});
}

function showUpdateBanner(current, latest) {
  if (document.getElementById('updateBanner')) return; // already showing
  const banner = document.createElement('div');
  banner.id = 'updateBanner';
  banner.className = 'update-banner';
  banner.innerHTML = '<span><strong>Update available:</strong> ' + esc(current) + ' → ' + esc(latest) +
    ' - Run <code>alf upgrade</code> on the host.</span>' +
    '<button class="update-banner-close" title="Dismiss">&times;</button>';
  banner.querySelector('.update-banner-close').addEventListener('click', () => {
    banner.remove();
    _updateDismissed = true;
  });
  document.body.prepend(banner);
}


// --- Settings: Re-run Setup Wizard ---
document.getElementById('settingsRerunSetup').addEventListener('click', () => {
  api('/api/setup/status').then(status => showSetupWizard(status)).catch(() => toast('Could not load setup status', 'error'));
});

// --- Admin Actions ---
(function() {
  const restartBtn = document.getElementById('adminRestartBtn');
  const outputEl = document.getElementById('adminOutput');
  const outputText = document.getElementById('adminOutputText');

  function showOutput(text) {
    outputText.textContent = text;
    outputEl.style.display = '';
  }

  restartBtn.addEventListener('click', async () => {
    if (!confirm('Restart the ALF daemon?')) return;
    restartBtn.disabled = true;
    try {
      await fetch('/api/restart', { method: 'POST', credentials: 'same-origin' });
    } catch (e) { /* expected — server is shutting down */ }
    waitForDaemonAndReload(showOutput);
  });

})();

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// --- Activity Monitor ---
(function() {
  const bar = document.getElementById('activityBar');
  const label = document.getElementById('activityLabel');
  const itemsEl = document.getElementById('activityItems');
  if (!bar) return;

  const typeIcons = {
    chat: 'message-circle',
    schedule: 'clock',
    task: 'cpu',
  };
  const typeLabels = {
    chat: 'Chat',
    schedule: 'Schedule',
    task: 'Task',
  };

  let lastJSON = '';

  async function poll() {
    try {
      const data = await api('/api/activity');
      const json = JSON.stringify(data.items || []);
      if (json === lastJSON) return; // skip DOM update if unchanged
      lastJSON = json;

      if (!data.items || data.items.length === 0) {
        bar.style.display = 'none';
        return;
      }

      bar.style.display = '';
      const n = data.items.length;
      label.textContent = n + ' active operation' + (n > 1 ? 's' : '');

      itemsEl.innerHTML = data.items.map(item => {
        const icon = typeIcons[item.type] || 'activity';
        const badge = typeLabels[item.type] || item.type;
        const elapsed = item.elapsed ? ' -' + esc(item.elapsed) : '';
        return '<div class="activity-item">' +
          '<i data-lucide="' + icon + '"></i>' +
          '<span class="activity-badge">' + esc(badge) + '</span>' +
          '<span class="activity-name">' + esc(item.name) + '</span>' +
          '<span class="activity-elapsed">' + elapsed + '</span>' +
          '</div>';
      }).join('');
      lucide.createIcons();
    } catch (e) {
      // silent -don't disrupt UI on transient errors
    }
  }

  poll();
  setInterval(poll, 5000);
})();

// --- Telegram Integration ---
(function() {
  const statusEl = document.getElementById('telegramStatus');
  const formEl = document.getElementById('telegramForm');
  const tokenInput = document.getElementById('tgBotToken');
  const chatIDInput = document.getElementById('tgChatID');
  const saveBtn = document.getElementById('tgSaveBtn');
  const cancelBtn = document.getElementById('tgCancelBtn');
  const disconnectBtn = document.getElementById('tgDisconnectBtn');
  const editBtn = document.getElementById('tgEditBtn');
  const resultEl = document.getElementById('tgResult');

  if (!statusEl) return;

  let isConfigured = false;

  function collapse() {
    formEl.style.display = 'none';
    editBtn.style.display = '';
    resultEl.style.display = 'none';
  }

  function expand() {
    formEl.style.display = '';
    editBtn.style.display = 'none';
    if (isConfigured) {
      cancelBtn.style.display = '';
      disconnectBtn.style.display = '';
    }
  }

  async function loadStatus() {
    try {
      const data = await api('/api/telegram');
      isConfigured = !!data.configured;
      if (data.configured) {
        statusEl.innerHTML = '<div class="tg-status tg-connected"><i data-lucide="check-circle"></i> Connected' +
          (data.bot_name ? ' -@' + esc(data.bot_name) : '') +
          (data.chat_id ? ' <span class="tg-detail">(chat ' + esc(data.chat_id) + ')</span>' : '') +
          '</div>';
        tokenInput.placeholder = data.bot_token_masked || '***';
        tokenInput.value = '';
        chatIDInput.value = data.chat_id || '';
        // Collapsed by default when configured.
        collapse();
        lucide.createIcons();
      } else {
        statusEl.innerHTML = '<div class="tg-status tg-disconnected"><i data-lucide="circle-off"></i> Not configured</div>';
        editBtn.style.display = 'none';
        cancelBtn.style.display = 'none';
        disconnectBtn.style.display = 'none';
        formEl.style.display = '';
        lucide.createIcons();
      }
    } catch (e) {
      statusEl.innerHTML = '<div class="tg-status tg-disconnected">Could not load Telegram status</div>';
      formEl.style.display = '';
    }
  }

  editBtn.addEventListener('click', expand);
  cancelBtn.addEventListener('click', () => {
    collapse();
    resultEl.style.display = 'none';
  });

  saveBtn.addEventListener('click', async () => {
    const token = tokenInput.value.trim();
    const chatID = chatIDInput.value.trim();
    if (!token || !chatID) {
      resultEl.style.display = '';
      resultEl.className = 'tg-result tg-error';
      resultEl.textContent = 'Both bot token and chat ID are required.';
      return;
    }
    saveBtn.disabled = true;
    saveBtn.textContent = 'Verifying...';
    try {
      const data = await api('/api/telegram', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ bot_token: token, chat_id: chatID })
      });
      resultEl.style.display = '';
      resultEl.className = 'tg-result tg-success';
      resultEl.innerHTML = 'Telegram connected to @' + esc(data.bot_name) + '.<br>' +
        '<strong>Restart ALF to activate.</strong> ' +
        '<button class="btn btn-sm" onclick="document.getElementById(\'adminRestartBtn\').click()">Restart now</button>';
      loadStatus();
    } catch (e) {
      resultEl.style.display = '';
      resultEl.className = 'tg-result tg-error';
      resultEl.textContent = e.error || e.message || 'Failed to save Telegram config.';
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = 'Save & Verify';
    }
  });

  disconnectBtn.addEventListener('click', async () => {
    if (!confirm('Disconnect Telegram? ALF will run in Control Center-only mode after restart.')) return;
    try {
      await api('/api/telegram', { method: 'DELETE' });
      resultEl.style.display = '';
      resultEl.className = 'tg-result tg-success';
      resultEl.innerHTML = 'Telegram disconnected. <strong>Restart ALF to apply.</strong> ' +
        '<button class="btn btn-sm" onclick="document.getElementById(\'adminRestartBtn\').click()">Restart now</button>';
      loadStatus();
    } catch (e) {
      resultEl.style.display = '';
      resultEl.className = 'tg-result tg-error';
      resultEl.textContent = 'Failed to disconnect.';
    }
  });

  loadStatus();
})();

// --- Workspace Explorer (Tree) ---
let wsOpenPath = null;
let wsTree = {};  // { [dirPath]: { loaded, expanded, entries[] } }
let wsProtectedDirs = [];  // populated from backend on root listing

// Lucide icon names by file extension.
const FILE_ICON_MAP = {
  '.md': 'file-text', '.txt': 'file-text',
  '.json': 'file-json', '.toml': 'file-cog', '.yaml': 'file-cog', '.yml': 'file-cog',
  '.sh': 'file-terminal', '.py': 'file-code', '.js': 'file-code',
  '.png': 'image', '.jpg': 'image', '.jpeg': 'image', '.gif': 'image', '.webp': 'image',
  '.mp3': 'file-audio', '.wav': 'file-audio', '.ogg': 'file-audio',
  '.mp4': 'file-video', '.webm': 'file-video',
};

// Validate a Lucide icon name; return fallback if not found in the loaded icon set.
function safeLucideIcon(name, fallback) {
  if (!window.lucide || !window.lucide.icons) return name;
  // Lucide stores icons in PascalCase; convert kebab-case for lookup.
  const pascal = name.replace(/(^|-)(\w)/g, (_, __, c) => c.toUpperCase());
  if (!window.lucide.icons[pascal]) {
    return fallback || 'box';
  }
  return name;
}

function wsIcon(name, cls) {
  return '<i data-lucide="' + safeLucideIcon(name) + '"' + (cls ? ' class="' + cls + '"' : '') + '></i>';
}

function fileIcon(filename) {
  const dot = filename.lastIndexOf('.');
  const ext = dot >= 0 ? filename.slice(dot).toLowerCase() : '';
  return FILE_ICON_MAP[ext] || 'file';
}

function wsInit() {
  wsTree = {};
  document.getElementById('wsBreadcrumb').innerHTML = wsBreadcrumbHTML('');
  wsToggleDir('');
}

function wsToggleDir(dirPath) {
  const node = wsTree[dirPath];
  if (node && node.loaded) {
    node.expanded = !node.expanded;
    wsRender();
    return;
  }
  api('/api/workspace?path=' + encodeURIComponent(dirPath)).then(r => {
    if (r.type !== 'directory') return;
    if (r.protected) wsProtectedDirs = r.protected;
    wsTree[dirPath] = { loaded: true, expanded: true, entries: r.entries || [] };
    wsRender();
  }).catch(() => {
    document.getElementById('wsFileList').innerHTML = '<div class="explorer-empty">Failed to load</div>';
  });
}

function wsRender() {
  const list = document.getElementById('wsFileList');
  const html = wsRenderDir('', 0);
  list.innerHTML = html || '<div class="explorer-empty">Empty</div>';

  // Bind handlers.
  list.querySelectorAll('.ws-node-dir').forEach(el => {
    el.addEventListener('click', () => wsToggleDir(el.dataset.path));
  });
  list.querySelectorAll('.ws-node-file').forEach(el => {
    el.addEventListener('click', () => wsOpenFile(el.dataset.path));
  });
  list.querySelectorAll('.ws-dir-delete').forEach(el => {
    el.addEventListener('click', (ev) => {
      ev.stopPropagation();
      wsDeleteDir(el.dataset.path);
    });
  });

  // Render Lucide icons in the tree.
  if (window.lucide) lucide.createIcons();
}

function wsRenderDir(dirPath, depth) {
  const node = wsTree[dirPath];
  if (!node || !node.loaded || !node.expanded) return '';

  let html = '';
  const entries = node.entries || [];

  entries.forEach((e, i) => {
    const fullPath = dirPath ? dirPath + '/' + e.name : e.name;
    const isLast = i === entries.length - 1;

    if (e.is_dir) {
      const childNode = wsTree[fullPath];
      const expanded = childNode && childNode.expanded;
      const chevronIcon = expanded ? 'chevron-down' : 'chevron-right';
      const folderIcon = expanded ? 'folder-open' : 'folder';

      const canDeleteDir = !wsProtectedDirs.includes(fullPath) && (depth > 0 || !wsProtectedDirs.includes(e.name));
      html += '<div class="ws-node ws-node-dir' + (expanded ? ' expanded' : '') + '" data-path="' + esc(fullPath) + '" style="padding-left:' + (8 + depth * 20) + 'px">' +
        wsIcon(chevronIcon, 'ws-icon ws-icon-chevron') +
        wsIcon(folderIcon, 'ws-icon ws-icon-folder') +
        '<span class="ws-node-label">' + esc(e.name) + '</span>' +
        (canDeleteDir ? '<span class="ws-dir-delete" data-path="' + esc(fullPath) + '" title="Delete folder">' + wsIcon('trash-2', 'ws-icon') + '</span>' : '') +
        '</div>';
      if (expanded) {
        html += wsRenderDir(fullPath, depth + 1);
      }
    } else {
      const active = wsOpenPath === fullPath ? ' active' : '';
      const icon = fileIcon(e.name);
      html += '<div class="ws-node ws-node-file' + active + '" data-path="' + esc(fullPath) + '" style="padding-left:' + (8 + depth * 20 + 20) + 'px">' +
        wsIcon(icon, 'ws-icon ws-icon-file') +
        '<span class="ws-node-label">' + esc(e.name) + '</span>' +
        '<span class="ws-node-size">' + formatSize(e.size) + '</span>' +
        '</div>';
    }
  });

  return html;
}

let wsViewMode = false;
let wsMdMode = 'preview'; // 'preview' or 'edit'

function wsResetViewer() {
  wsViewMode = false;
  wsMdMode = 'preview';
  const viewBtn = document.getElementById('wsViewBtn');
  const viewer = document.getElementById('wsViewer');
  const mdPreview = document.getElementById('wsMdPreview');
  const mdEditBtn = document.getElementById('wsMdEditBtn');
  viewBtn.style.display = 'none';
  viewer.style.display = 'none';
  viewer.innerHTML = '';
  mdPreview.style.display = 'none';
  mdPreview.innerHTML = '';
  mdEditBtn.style.display = 'none';
  jvLiveData = null;
  viewBtn.innerHTML = '<i data-lucide="sliders"></i>';
  viewBtn.title = 'Form view';
}

function wsIsMdFile(path) {
  return path && path.toLowerCase().endsWith('.md');
}

function wsShowMdPreview(content) {
  const mdPreview = document.getElementById('wsMdPreview');
  const editor = document.getElementById('wsEditor');
  mdPreview.innerHTML = chatRenderMd(content || '');
  mdPreview.style.display = '';
  editor.style.display = 'none';
  wsMdMode = 'preview';
  const btn = document.getElementById('wsMdEditBtn');
  btn.innerHTML = '<i data-lucide="pencil"></i>';
  btn.title = 'Edit';
  if (window.lucide) lucide.createIcons();
}

function wsShowMdEditor() {
  const mdPreview = document.getElementById('wsMdPreview');
  const editor = document.getElementById('wsEditor');
  mdPreview.style.display = 'none';
  editor.style.display = '';
  editor.focus();
  wsMdMode = 'edit';
  const btn = document.getElementById('wsMdEditBtn');
  btn.innerHTML = '<i data-lucide="eye"></i>';
  btn.title = 'Preview';
  if (window.lucide) lucide.createIcons();
}

function wsIsJsonFile(path) {
  if (!path) return false;
  const lower = path.toLowerCase();
  return lower.endsWith('.json') || lower.endsWith('.jsonl');
}

function wsOpenFile(filePath) {
  wsOpenPath = filePath;
  const editor = document.getElementById('wsEditor');
  const msg = document.getElementById('wsMessage');
  const saveBtn = document.getElementById('wsSaveBtn');
  const deleteBtn = document.getElementById('wsDeleteBtn');

  deleteBtn.disabled = true;
  wsResetViewer();

  wsRender();

  document.getElementById('wsBreadcrumb').innerHTML = wsBreadcrumbHTML(filePath);
  document.getElementById('wsBreadcrumb').querySelectorAll('.ws-bc-link').forEach(bcEl => {
    bcEl.addEventListener('click', () => wsExpandTo(bcEl.dataset.path));
  });
  if (window.lucide) lucide.createIcons();

  api('/api/workspace?path=' + encodeURIComponent(filePath)).then(r => {
    document.getElementById('wsFileName').textContent = r.name || filePath.split('/').pop();

    if (r.message) {
      editor.style.display = 'none';
      msg.style.display = 'flex';
      msg.textContent = r.message;
      saveBtn.disabled = true;
      deleteBtn.disabled = true;
      return;
    }

    msg.style.display = 'none';
    editor.style.display = '';
    editor.value = r.content || '';
    editor.disabled = !r.editable;
    saveBtn.disabled = !r.editable;
    deleteBtn.disabled = !r.editable;

    // Show pretty-view toggle for JSON/JSONL files
    if (wsIsJsonFile(filePath)) {
      document.getElementById('wsViewBtn').style.display = '';
      if (window.lucide) lucide.createIcons();
    }

    // Markdown files: show preview by default with edit toggle
    if (wsIsMdFile(filePath)) {
      document.getElementById('wsMdEditBtn').style.display = '';
      wsShowMdPreview(r.content || '');
    }
  }).catch(() => toast('Failed to load file', 'error'));
}

function wsBreadcrumbHTML(path) {
  const parts = path ? path.split('/').filter(Boolean) : [];
  let html = '<span class="ws-bc-item ws-bc-link" data-path="">' + wsIcon('database', 'ws-icon') + ' data</span>';
  let accumulated = '';
  parts.forEach((p, i) => {
    accumulated = accumulated ? accumulated + '/' + p : p;
    const isLast = i === parts.length - 1;
    html += wsIcon('chevron-right', 'ws-icon ws-bc-sep');
    if (isLast) {
      html += '<span class="ws-bc-item ws-bc-current">' + esc(p) + '</span>';
    } else {
      html += '<span class="ws-bc-item ws-bc-link" data-path="' + esc(accumulated) + '">' + esc(p) + '</span>';
    }
  });
  return html;
}

function wsExpandTo(dirPath) {
  if (!dirPath) {
    wsOpenPath = null;
    document.getElementById('wsFileName').textContent = 'Select a file';
    document.getElementById('wsSaveBtn').disabled = true;
    document.getElementById('wsEditor').style.display = 'none';
    document.getElementById('wsMessage').style.display = 'none';
    wsResetViewer();
    document.getElementById('wsBreadcrumb').innerHTML = wsBreadcrumbHTML('');
    if (window.lucide) lucide.createIcons();
    return;
  }
  const parts = dirPath.split('/').filter(Boolean);
  let current = '';
  (async () => {
    for (const p of parts) {
      if (!wsTree[current] || !wsTree[current].loaded) {
        await api('/api/workspace?path=' + encodeURIComponent(current)).then(r => {
          if (r.type === 'directory') {
            wsTree[current] = { loaded: true, expanded: true, entries: r.entries || [] };
          }
        });
      } else {
        wsTree[current].expanded = true;
      }
      current = current ? current + '/' + p : p;
    }
    if (!wsTree[current] || !wsTree[current].loaded) {
      await api('/api/workspace?path=' + encodeURIComponent(current)).then(r => {
        if (r.type === 'directory') {
          wsTree[current] = { loaded: true, expanded: true, entries: r.entries || [] };
        }
      });
    } else {
      wsTree[current].expanded = true;
    }
    wsRender();
  })();
}

document.getElementById('wsDeleteBtn').addEventListener('click', () => {
  if (!wsOpenPath) return;
  const name = wsOpenPath.split('/').pop();
  if (!confirm('Delete ' + name + '?')) return;
  api('/api/workspace?path=' + encodeURIComponent(wsOpenPath), {
    method: 'DELETE',
  }).then(r => {
    if (r.ok) {
      toast('Deleted');
      // Refresh the parent directory in the tree.
      const parentPath = wsOpenPath.includes('/') ? wsOpenPath.substring(0, wsOpenPath.lastIndexOf('/')) : '';
      wsOpenPath = null;
      document.getElementById('wsFileName').textContent = 'Select a file';
      document.getElementById('wsSaveBtn').disabled = true;
      document.getElementById('wsDeleteBtn').disabled = true;
      document.getElementById('wsEditor').style.display = 'none';
      document.getElementById('wsMessage').style.display = 'none';
      wsResetViewer();
      document.getElementById('wsBreadcrumb').innerHTML = wsBreadcrumbHTML('');
      if (window.lucide) lucide.createIcons();
      // Re-fetch parent dir to update tree.
      delete wsTree[parentPath];
      wsToggleDir(parentPath);
    } else toast(r.error || 'Delete failed', 'error');
  }).catch(e => toast(e.error || 'Delete failed', 'error'));
});

function wsDeleteDir(dirPath) {
  const name = dirPath.split('/').pop();
  if (!confirm('Delete folder "' + name + '" and all its contents?')) return;
  api('/api/workspace?path=' + encodeURIComponent(dirPath), { method: 'DELETE' })
    .then(r => {
      if (r.ok) {
        toast('Folder deleted');
        // If an open file was inside this folder, clear the editor.
        if (wsOpenPath && wsOpenPath.startsWith(dirPath + '/')) {
          wsOpenPath = null;
          document.getElementById('wsFileName').textContent = 'Select a file';
          document.getElementById('wsSaveBtn').disabled = true;
          document.getElementById('wsDeleteBtn').disabled = true;
          document.getElementById('wsEditor').style.display = 'none';
          document.getElementById('wsMessage').style.display = 'none';
          wsResetViewer();
          document.getElementById('wsBreadcrumb').innerHTML = wsBreadcrumbHTML('');
          if (window.lucide) lucide.createIcons();
        }
        // Remove from tree cache and refresh parent.
        delete wsTree[dirPath];
        const parentPath = dirPath.includes('/') ? dirPath.substring(0, dirPath.lastIndexOf('/')) : '';
        delete wsTree[parentPath];
        wsToggleDir(parentPath);
      } else {
        toast(r.error || 'Delete failed', 'error');
      }
    })
    .catch(e => toast(e.error || 'Delete failed', 'error'));
}

document.getElementById('wsSaveBtn').addEventListener('click', () => {
  if (!wsOpenPath) return;
  const content = document.getElementById('wsEditor').value;
  api('/api/workspace?path=' + encodeURIComponent(wsOpenPath), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  }).then(r => {
    if (r.ok) toast('Saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
});

// --- Markdown Edit Toggle ---
document.getElementById('wsMdEditBtn').addEventListener('click', () => {
  if (wsMdMode === 'preview') {
    wsShowMdEditor();
  } else {
    // Switching to preview: auto-save first
    const editor = document.getElementById('wsEditor');
    wsShowMdPreview(editor.value);
    if (wsOpenPath && !editor.disabled) {
      // Auto-save in background
      api('/api/workspace', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: wsOpenPath, content: editor.value }),
      }).catch(() => {}); // silent save
    }
  }
});

// Auto-save markdown on blur (switching away from editor)
document.getElementById('wsEditor').addEventListener('blur', () => {
  if (wsIsMdFile(wsOpenPath) && wsMdMode === 'edit') {
    const editor = document.getElementById('wsEditor');
    if (!editor.disabled) {
      api('/api/workspace', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: wsOpenPath, content: editor.value }),
      }).catch(() => {});
    }
  }
});

// --- JSON Pretty Viewer ---
document.getElementById('wsViewBtn').addEventListener('click', () => {
  wsViewMode = !wsViewMode;
  const editor = document.getElementById('wsEditor');
  const viewer = document.getElementById('wsViewer');
  const btn = document.getElementById('wsViewBtn');

  if (wsViewMode) {
    editor.style.display = 'none';
    viewer.style.display = '';
    viewer.innerHTML = '';
    const content = editor.value;
    if (wsOpenPath && wsOpenPath.toLowerCase().endsWith('.jsonl')) {
      viewer.innerHTML = renderJsonl(content);
    } else {
      try {
        jvLiveData = JSON.parse(content);
        viewer.appendChild(renderJsonNode(jvLiveData, [], 0));
      } catch (e) {
        jvLiveData = null;
        viewer.innerHTML = '<span class="jv-null">Invalid JSON: ' + esc(e.message) + '</span>';
      }
    }
    btn.innerHTML = '<i data-lucide="code"></i>';
    btn.title = 'Code mode';
  } else {
    jvLiveData = null;
    viewer.style.display = 'none';
    viewer.innerHTML = '';
    editor.style.display = '';
    btn.innerHTML = '<i data-lucide="sliders"></i>';
    btn.title = 'Form view';
  }
  if (window.lucide) lucide.createIcons();
});

// --- JSON Interactive Editor ---
// Stores the live data model; edits sync back to the text editor on change.
let jvLiveData = null;

function jvSyncToEditor() {
  if (jvLiveData === null) return;
  const editor = document.getElementById('wsEditor');
  editor.value = JSON.stringify(jvLiveData, null, 2);
}

// Build an interactive DOM tree for a JSON value.
// path is an array of keys/indices into jvLiveData.
function renderJsonNode(val, path, depth) {
  const el = document.createElement('div');
  el.className = 'jv-node';
  el.style.setProperty('--depth', depth);

  if (val === null) {
    el.innerHTML = '<span class="jv-null">null</span>';
    return el;
  }

  if (typeof val === 'boolean') {
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = val;
    cb.className = 'jv-input-bool';
    cb.addEventListener('change', () => {
      jvSetPath(path, cb.checked);
      jvSyncToEditor();
    });
    el.appendChild(cb);
    return el;
  }

  if (typeof val === 'number') {
    const wrap = document.createElement('span');
    wrap.className = 'jv-num-wrap';
    const inp = document.createElement('input');
    inp.type = 'number';
    inp.className = 'jv-input-num';
    inp.value = val;
    inp.step = Number.isInteger(val) ? 1 : 0.1;
    inp.addEventListener('change', () => {
      const n = parseFloat(inp.value);
      if (!isNaN(n)) { jvSetPath(path, n); jvSyncToEditor(); }
    });
    wrap.appendChild(inp);
    el.appendChild(wrap);
    return el;
  }

  if (typeof val === 'string') {
    // Detect ISO 8601 / RFC 3339 datetime strings.
    const dtMatch = val.match(/^(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2}(:\d{2})?)/);
    if (dtMatch) {
      const wrap = document.createElement('span');
      wrap.className = 'jv-dt-wrap';
      const inp = document.createElement('input');
      inp.type = 'datetime-local';
      inp.className = 'jv-input-dt';
      // datetime-local expects "YYYY-MM-DDTHH:MM:SS" without timezone.
      const d = new Date(val);
      if (!isNaN(d.getTime())) {
        // Format as local datetime for the picker.
        const pad = n => String(n).padStart(2, '0');
        inp.value = d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate())
          + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
      } else {
        inp.value = dtMatch[1] + 'T' + dtMatch[2];
      }
      inp.step = 1; // show seconds
      inp.addEventListener('change', () => {
        // Preserve original timezone suffix if present.
        const tzMatch = val.match(/([+-]\d{2}:\d{2}|Z)$/);
        const tz = tzMatch ? tzMatch[1] : '';
        const newVal = inp.value.replace('T', 'T') + (tz || '');
        jvSetPath(path, newVal);
        jvSyncToEditor();
      });
      wrap.appendChild(inp);
      el.appendChild(wrap);
      return el;
    }

    // Multi-line strings get a textarea, short ones get input.
    if (val.length > 80 || val.includes('\n')) {
      const ta = document.createElement('textarea');
      ta.className = 'jv-input-text jv-input-textarea';
      ta.value = val;
      ta.rows = Math.min(6, val.split('\n').length + 1);
      ta.addEventListener('change', () => { jvSetPath(path, ta.value); jvSyncToEditor(); });
      el.appendChild(ta);
    } else {
      const inp = document.createElement('input');
      inp.type = 'text';
      inp.className = 'jv-input-text';
      inp.value = val;
      inp.addEventListener('change', () => { jvSetPath(path, inp.value); jvSyncToEditor(); });
      el.appendChild(inp);
    }
    return el;
  }

  if (Array.isArray(val)) {
    const header = document.createElement('div');
    header.className = 'jv-section-header';
    const toggle = document.createElement('span');
    toggle.className = 'jv-toggle';
    toggle.textContent = '▼';
    const badge = document.createElement('span');
    badge.className = 'jv-badge';
    badge.textContent = val.length + ' item' + (val.length !== 1 ? 's' : '');
    header.appendChild(toggle);
    header.appendChild(badge);
    el.appendChild(header);

    const children = document.createElement('div');
    children.className = 'jv-children';
    val.forEach((item, i) => {
      const row = document.createElement('div');
      row.className = 'jv-field';
      const label = document.createElement('span');
      label.className = 'jv-index';
      label.textContent = '[' + i + ']';
      row.appendChild(label);
      row.appendChild(renderJsonNode(item, path.concat(i), depth + 1));
      children.appendChild(row);
    });
    el.appendChild(children);

    toggle.addEventListener('click', () => {
      children.classList.toggle('collapsed');
      toggle.textContent = children.classList.contains('collapsed') ? '▶' : '▼';
    });
    return el;
  }

  if (typeof val === 'object') {
    const keys = Object.keys(val);
    const header = document.createElement('div');
    header.className = 'jv-section-header';
    const toggle = document.createElement('span');
    toggle.className = 'jv-toggle';
    toggle.textContent = '▼';
    const badge = document.createElement('span');
    badge.className = 'jv-badge';
    badge.textContent = keys.length + ' field' + (keys.length !== 1 ? 's' : '');
    header.appendChild(toggle);
    header.appendChild(badge);
    el.appendChild(header);

    const children = document.createElement('div');
    children.className = 'jv-children';
    keys.forEach(key => {
      const row = document.createElement('div');
      row.className = 'jv-field';
      const label = document.createElement('label');
      label.className = 'jv-key';
      label.textContent = key;
      row.appendChild(label);
      row.appendChild(renderJsonNode(val[key], path.concat(key), depth + 1));
      children.appendChild(row);
    });
    el.appendChild(children);

    toggle.addEventListener('click', () => {
      children.classList.toggle('collapsed');
      toggle.textContent = children.classList.contains('collapsed') ? '▶' : '▼';
    });
    return el;
  }

  el.textContent = String(val);
  return el;
}

// Set a value deep in jvLiveData by path array.
function jvSetPath(path, value) {
  let obj = jvLiveData;
  for (let i = 0; i < path.length - 1; i++) {
    obj = obj[path[i]];
  }
  obj[path[path.length - 1]] = value;
}

// Legacy render for read-only contexts (JSONL lines).
function renderJsonValue(val, depth) {
  if (val === null) return '<span class="jv-null">null</span>';
  if (typeof val === 'string') return '<span class="jv-string">"' + esc(val) + '"</span>';
  if (typeof val === 'number') return '<span class="jv-number">' + val + '</span>';
  if (typeof val === 'boolean') return '<span class="jv-bool">' + val + '</span>';

  if (Array.isArray(val)) {
    if (val.length === 0) return '<span class="jv-bracket">[]</span>';
    const id = 'jv-' + Math.random().toString(36).slice(2, 8);
    let html = '<span class="jv-toggle" onclick="jvToggle(\'' + id + '\',this)">▼</span>';
    html += '<span class="jv-bracket">[</span>';
    html += '<div class="jv-children" id="' + id + '">';
    val.forEach((item, i) => {
      html += '<div class="jv-row" style="--depth:' + (depth + 1) + '">';
      html += renderJsonValue(item, depth + 1);
      if (i < val.length - 1) html += ',';
      html += '</div>';
    });
    html += '</div>';
    html += '<div class="jv-row" style="--depth:' + depth + '"><span class="jv-bracket">]</span></div>';
    return html;
  }

  if (typeof val === 'object') {
    const keys = Object.keys(val);
    if (keys.length === 0) return '<span class="jv-bracket">{}</span>';
    const id = 'jv-' + Math.random().toString(36).slice(2, 8);
    let html = '<span class="jv-toggle" onclick="jvToggle(\'' + id + '\',this)">▼</span>';
    html += '<span class="jv-bracket">{</span>';
    html += '<div class="jv-children" id="' + id + '">';
    keys.forEach((key, i) => {
      html += '<div class="jv-row" style="--depth:' + (depth + 1) + '">';
      html += '<span class="jv-key">"' + esc(key) + '"</span>: ';
      html += renderJsonValue(val[key], depth + 1);
      if (i < keys.length - 1) html += ',';
      html += '</div>';
    });
    html += '</div>';
    html += '<div class="jv-row" style="--depth:' + depth + '"><span class="jv-bracket">}</span></div>';
    return html;
  }

  return esc(String(val));
}

// --- Drag & Drop Upload ---
let wsPendingFiles = [];
let wsDragCounter = 0;

const wsCard = document.getElementById('workspaceCard');
const wsOverlay = document.getElementById('wsDropOverlay');

wsCard.addEventListener('dragenter', (e) => {
  e.preventDefault();
  wsDragCounter++;
  wsOverlay.classList.add('active');
});
wsCard.addEventListener('dragover', (e) => e.preventDefault());
wsCard.addEventListener('dragleave', (e) => {
  e.preventDefault();
  wsDragCounter--;
  if (wsDragCounter <= 0) {
    wsDragCounter = 0;
    wsOverlay.classList.remove('active');
  }
});
wsCard.addEventListener('drop', (e) => {
  e.preventDefault();
  wsDragCounter = 0;
  wsOverlay.classList.remove('active');

  const items = e.dataTransfer.items;
  const files = [];
  const paths = [];

  // Try to get entries for folder structure preservation.
  if (items && items.length > 0 && items[0].webkitGetAsEntry) {
    const entries = [];
    for (let i = 0; i < items.length; i++) {
      const entry = items[i].webkitGetAsEntry();
      if (entry) entries.push(entry);
    }
    wsReadEntries(entries, '').then(result => {
      if (result.length === 0) return;
      wsPendingFiles = result;
      wsShowUploadModal();
    });
  } else {
    // Fallback: flat file list.
    for (const f of e.dataTransfer.files) {
      files.push({ file: f, path: f.name });
    }
    if (files.length === 0) return;
    wsPendingFiles = files;
    wsShowUploadModal();
  }
});

function wsReadEntries(entries, prefix) {
  const results = [];
  const promises = [];
  for (const entry of entries) {
    if (entry.isFile) {
      promises.push(new Promise(resolve => {
        entry.file(f => {
          results.push({ file: f, path: prefix ? prefix + '/' + f.name : f.name });
          resolve();
        });
      }));
    } else if (entry.isDirectory) {
      promises.push(new Promise(resolve => {
        const reader = entry.createReader();
        reader.readEntries(subEntries => {
          const subPrefix = prefix ? prefix + '/' + entry.name : entry.name;
          wsReadEntries(subEntries, subPrefix).then(sub => {
            results.push(...sub);
            resolve();
          });
        });
      }));
    }
  }
  return Promise.all(promises).then(() => results);
}

function wsShowUploadModal() {
  const modal = document.getElementById('uploadModal');
  const list = document.getElementById('uploadFileList');
  const target = document.getElementById('uploadTarget');

  // Populate file list.
  list.innerHTML = wsPendingFiles.map(f =>
    '<div class="upload-file-item"><span class="name">' + esc(f.path) + '</span><span class="size">' + formatSize(f.file.size) + '</span></div>'
  ).join('');

  // Populate target options from known directories.
  const dirs = ['skills.d', 'tools', 'context.d', 'config.d', 'memory.d', 'apps'];
  target.innerHTML = dirs.map(d => '<option value="' + d + '">' + d + '</option>').join('');
  // Add custom option.
  target.innerHTML += '<option value="__custom">Other...</option>';

  // Pre-select skills.d since that's the primary use case.
  target.value = 'skills.d';
  document.getElementById('uploadCustomRow').style.display = 'none';
  document.getElementById('uploadSubfolderRow').style.display = '';
  document.getElementById('uploadSubfolder').value = '';

  target.onchange = () => {
    const isCustom = target.value === '__custom';
    document.getElementById('uploadCustomRow').style.display = isCustom ? '' : 'none';
    document.getElementById('uploadSubfolderRow').style.display = isCustom ? 'none' : '';
  };

  modal.style.display = '';
  if (window.lucide) lucide.createIcons();
}

document.getElementById('uploadCancel').addEventListener('click', () => {
  document.getElementById('uploadModal').style.display = 'none';
  wsPendingFiles = [];
});

document.getElementById('uploadConfirm').addEventListener('click', async () => {
  const targetSel = document.getElementById('uploadTarget');
  let target = targetSel.value;
  if (target === '__custom') {
    const customPath = document.getElementById('uploadCustomPath').value.trim();
    if (!customPath) { toast('Enter a destination path', 'error'); return; }
    target = customPath;
  } else {
    const subfolder = document.getElementById('uploadSubfolder').value.trim();
    if (subfolder) target = target + '/' + subfolder;
  }

  const progress = document.getElementById('uploadProgress');
  const bar = document.getElementById('uploadProgressBar');
  const confirmBtn = document.getElementById('uploadConfirm');

  confirmBtn.disabled = true;
  progress.style.display = '';
  bar.style.width = '0%';

  const formData = new FormData();
  formData.append('target', target);
  for (const f of wsPendingFiles) {
    formData.append('files', f.file, f.file.name);
    formData.append('paths', f.path);
  }

  try {
    const resp = await fetch('/api/workspace/upload', {
      method: 'POST',
      body: formData,
      credentials: 'same-origin',
    });
    bar.style.width = '100%';

    if (resp.status === 401) {
      toast('Session expired', 'error');
      confirmBtn.disabled = false;
      progress.style.display = 'none';
      return;
    }

    const result = await resp.json();
    if (result.ok) {
      toast(wsPendingFiles.length + ' file(s) uploaded');
      document.getElementById('uploadModal').style.display = 'none';
      wsPendingFiles = [];

      // Refresh the target directory in the tree.
      const topDir = target.split('/')[0];
      delete wsTree[topDir];
      if (target !== topDir) delete wsTree[target];
      wsExpandTo(target);
    } else {
      toast(result.error || 'Upload failed', 'error');
    }
  } catch (e) {
    toast('Upload failed', 'error');
  }

  confirmBtn.disabled = false;
  progress.style.display = 'none';
});

function renderJsonl(content) {
  const lines = content.split('\n').filter(l => l.trim());
  if (lines.length === 0) return '<span class="jv-null">Empty file</span>';
  let html = '';
  lines.forEach((line, i) => {
    html += '<div class="jsonl-entry">';
    html += '<span class="jsonl-badge">Line ' + (i + 1) + '</span>';
    try {
      const parsed = JSON.parse(line);
      html += renderJsonValue(parsed, 0);
    } catch (e) {
      html += '<span class="jv-null">Invalid JSON: ' + esc(e.message) + '</span>';
    }
    html += '</div>';
  });
  return html;
}

window.jvToggle = function(id, el) {
  const children = document.getElementById(id);
  if (!children) return;
  children.classList.toggle('collapsed');
  el.textContent = children.classList.contains('collapsed') ? '▶' : '▼';
};

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  return (bytes / 1024).toFixed(1) + ' KB';
}


// --- Teach ---
const teachPreset = document.getElementById('teachPreset');
const teachCustomRow = document.getElementById('teachCustomRow');
const teachCustomInput = document.getElementById('teachCustomInput');
const teachTier = document.getElementById('teachTier');
const teachTierRow = document.getElementById('teachTierRow');
const teachContent = document.getElementById('teachContent');
const teachCounter = document.getElementById('teachCounter');
const teachSubmit = document.getElementById('teachSubmit');
const teachResult = document.getElementById('teachResult');
const teachFileNameRow = document.getElementById('teachFileNameRow');
const teachFileName = document.getElementById('teachFileName');
const TEACH_MAX = 50 * 1024;

let teachDest = 'memory';

const memoryPresets = [
  { value: 'Extract key facts', label: 'Extract key facts' },
  { value: 'Extract preferences', label: 'Extract preferences' },
  { value: 'Extract decisions', label: 'Extract decisions' },
  { value: 'store-as-is', label: 'Store as-is (one per line)' },
  { value: 'custom', label: 'Custom...' },
];
const contextPresets = [
  { value: 'store-as-is', label: 'Store as-is' },
  { value: 'summarize', label: 'Summarize' },
];

function setPresetOptions(presets) {
  teachPreset.innerHTML = '';
  presets.forEach(p => {
    const opt = document.createElement('option');
    opt.value = p.value;
    opt.textContent = p.label;
    teachPreset.appendChild(opt);
  });
  teachPreset.dispatchEvent(new Event('change'));
}

document.querySelectorAll('#teachDestination .seg-btn').forEach(btn => {
  btn.addEventListener('click', () => {
    document.querySelectorAll('#teachDestination .seg-btn').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
    teachDest = btn.dataset.value;
    teachFileNameRow.style.display = teachDest === 'context' ? '' : 'none';
    teachSubmit.textContent = teachDest === 'context' ? 'Save' : 'Import';
    setPresetOptions(teachDest === 'context' ? contextPresets : memoryPresets);
  });
});

function loadTeachTiers() {
  api('/api/memory/tiers').then(tiers => {
    teachTier.innerHTML = '';
    if (!tiers.length) {
      teachTier.innerHTML = '<option value="">No tiers</option>';
      return;
    }
    // Default: first tier with tools
    let defaultSet = false;
    tiers.forEach(t => {
      const opt = document.createElement('option');
      opt.value = t.name;
      opt.textContent = t.name + ' (' + t.model + ')';
      if (t.tools && !defaultSet) { opt.selected = true; defaultSet = true; }
      teachTier.appendChild(opt);
    });
  }).catch(() => {
    teachTier.innerHTML = '<option value="">Unavailable</option>';
  });
}

teachPreset.onchange = () => {
  teachCustomRow.style.display = teachPreset.value === 'custom' ? '' : 'none';
  // Hide tier selector for store-as-is (no Claude call needed)
  teachTierRow.style.display = teachPreset.value === 'store-as-is' ? 'none' : '';
};

teachContent.addEventListener('input', () => {
  const len = new Blob([teachContent.value]).size;
  const kb = (len / 1024).toFixed(1);
  teachCounter.textContent = kb + 'KB / 50KB';
  teachCounter.className = 'teach-counter' + (len > TEACH_MAX ? ' over' : len > TEACH_MAX * 0.9 ? ' warn' : '');
  teachSubmit.disabled = len > TEACH_MAX;
});

teachSubmit.addEventListener('click', () => {
  const content = teachContent.value.trim();
  if (!content) { toast('Paste some content first', 'error'); return; }

  let instruction = teachPreset.value;
  if (instruction === 'custom') {
    instruction = teachCustomInput.value.trim();
    if (!instruction) { toast('Enter a custom instruction', 'error'); return; }
  }

  // Context destination validation.
  if (teachDest === 'context') {
    const fn = teachFileName.value.trim();
    if (!fn) { toast('Enter a file name', 'error'); return; }
    if (!/^[a-zA-Z0-9_-]+$/.test(fn)) { toast('File name: only letters, numbers, dashes, underscores', 'error'); return; }
  }

  const tier = instruction === 'store-as-is' ? '' : teachTier.value;

  const payload = { content, instruction, tier };
  if (teachDest === 'context') {
    payload.destination = 'context';
    payload.file_name = teachFileName.value.trim();
  }

  teachSubmit.disabled = true;
  const isContext = teachDest === 'context';
  teachSubmit.textContent = instruction === 'store-as-is' ? 'Storing...' : 'Extracting...';
  if (isContext) teachSubmit.textContent = 'Saving...';
  teachResult.textContent = '';
  teachResult.className = 'teach-result';

  api('/api/memory/ingest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  }).then(r => {
    if (isContext) {
      teachResult.textContent = 'Saved to context/' + r.file_name;
    } else {
      teachResult.textContent = r.imported + ' imported, ' + (r.skipped || 0) + ' skipped';
    }
    teachResult.className = 'teach-result ok';
    if (r.imported > 0) teachContent.value = '';
    teachContent.dispatchEvent(new Event('input'));
  }).catch(e => {
    teachResult.textContent = e.error || 'Import failed';
    teachResult.className = 'teach-result err';
  }).finally(() => {
    teachSubmit.disabled = false;
    teachSubmit.textContent = isContext ? 'Save' : 'Import';
  });
});

// --- Chat badge (unread indicator) ---
function chatIsChatActive() {
  const navEl = document.querySelector('#navGrid .nav-item[data-view="chat"]');
  return navEl && navEl.classList.contains('active');
}
function chatShowBadge() {
  if (chatIsChatActive()) return;
  const navEl = document.querySelector('#navGrid .nav-item[data-view="chat"]');
  if (navEl) navEl.classList.add('has-badge');
  const bottomEl = document.querySelector('#bottomNav .bottom-nav-item[data-view="chat"]');
  if (bottomEl) bottomEl.classList.add('has-badge');
}
function chatClearBadge() {
  const navEl = document.querySelector('#navGrid .nav-item[data-view="chat"]');
  if (navEl) navEl.classList.remove('has-badge');
  const bottomEl = document.querySelector('#bottomNav .bottom-nav-item[data-view="chat"]');
  if (bottomEl) bottomEl.classList.remove('has-badge');
}

// --- Chat Tabs ---
// Each tab stores its own state. The "active" tab's state is the live global vars below.
// On tab switch we snapshot globals → old tab, restore globals ← new tab.
let chatTabList = []; // [{id, convId, title}]
let chatActiveTabId = null;
const chatTabUnread = new Set(); // tab IDs with unread responses
// DOM cache: tabId → { html, scrollTop }
const chatTabDOMCache = {};

function chatGenerateTabId() {
  return 'tab-' + Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
}

function chatSaveTabs() {
  localStorage.setItem('alf-chat-tabs', JSON.stringify(chatTabList));
  localStorage.setItem('alf-chat-active-tab', chatActiveTabId || '');
}

function chatLoadTabs() {
  try {
    const raw = localStorage.getItem('alf-chat-tabs');
    if (raw) chatTabList = JSON.parse(raw);
  } catch { chatTabList = []; }
  chatActiveTabId = localStorage.getItem('alf-chat-active-tab') || '';
}

// Snapshot current global chat state into DOM cache for the active tab.
function chatSnapshotTab() {
  if (!chatActiveTabId) return;
  chatTabDOMCache[chatActiveTabId] = {
    html: chatMessages.innerHTML,
    scrollTop: chatMessages.scrollTop,
    // Stream state -preserved so in-flight streams survive tab switches.
    sending: chatSending,
    jobId: chatJobId,
    eventOffset: chatEventOffset,
    historyLoaded: chatHistoryLoaded,
  };
}

// Restore global state from DOM cache for a tab.
function chatRestoreTab(tabId) {
  const cached = chatTabDOMCache[tabId];
  if (cached) {
    chatMessages.innerHTML = cached.html;
    chatMessages.scrollTop = cached.scrollTop;
    chatSending = cached.sending || false;
    chatJobId = cached.jobId || null;
    chatEventOffset = cached.eventOffset || 0;
    chatHistoryLoaded = cached.historyLoaded || false;
  } else {
    chatMessages.innerHTML = '';
    chatSending = false;
    chatJobId = null;
    chatEventOffset = 0;
    chatHistoryLoaded = false;
  }
  // Reset stream DOM refs ONLY if there is no active stream running in the
  // background (detached container). If a stream is detached, its DOM refs
  // must stay intact so chatProcessStream can keep writing to them.
  if (!chatDetachedContainer) {
    chatAssistantBubble = null;
    chatFullText = '';
    chatDoneData = null;
    chatReaction = null;
    chatStreamingText = false;
    chatThinkingEl = null;
    chatCurrentToolBlock = null;
    chatCurrentToolInput = '';
    chatAgentTracker = null;
    chatAgentTrackerBody = null;
    chatAgentStepCount = 0;
    chatCurrentTier = '';
    chatNeedNewBubble = false;
  }
}

function chatRenderTabs() {
  const list = document.getElementById('chatTabsList');
  list.innerHTML = '';
  chatTabList.forEach(tab => {
    const el = document.createElement('button');
    el.className = 'chat-tab' + (tab.id === chatActiveTabId ? ' active' : '');
    el.dataset.tabId = tab.id;
    const label = document.createElement('span');
    label.className = 'chat-tab-label';
    label.textContent = tab.title || 'New chat';
    el.appendChild(label);
    // Unread badge.
    if (chatTabUnread.has(tab.id)) {
      const badge = document.createElement('span');
      badge.className = 'chat-tab-badge';
      el.appendChild(badge);
    }
    // Close button (only if more than 1 tab).
    if (chatTabList.length > 1) {
      const close = document.createElement('span');
      close.className = 'chat-tab-close';
      close.textContent = '\u00d7';
      close.addEventListener('click', (e) => {
        e.stopPropagation();
        chatCloseTab(tab.id);
      });
      el.appendChild(close);
    }
    el.addEventListener('click', () => chatSwitchTab(tab.id));
    el.addEventListener('auxclick', (e) => {
      if (e.button === 1 && chatTabList.length > 1) {
        e.preventDefault();
        chatCloseTab(tab.id);
      }
    });
    el.addEventListener('dblclick', (e) => {
      e.preventDefault();
      chatRenameTab(tab.id, label);
    });
    list.appendChild(el);
  });
}

function chatRenameTab(tabId, labelEl) {
  const tab = chatTabList.find(t => t.id === tabId);
  if (!tab) return;
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'chat-tab-rename';
  input.value = tab.title || '';
  input.style.cssText = 'width:' + Math.max(60, labelEl.offsetWidth + 8) + 'px;font-size:inherit;padding:0 2px;border:1px solid var(--accent);border-radius:3px;background:var(--bg-input);color:var(--text);outline:none;';
  labelEl.replaceWith(input);
  input.focus();
  input.select();
  const commit = () => {
    const val = input.value.trim();
    if (val) tab.title = val;
    chatRenderTabs();
    chatSaveTabs();
  };
  input.addEventListener('blur', commit);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
    if (e.key === 'Escape') { input.value = tab.title || ''; input.blur(); }
  });
}

function chatSwitchTab(tabId) {
  if (tabId === chatActiveTabId) return;
  const oldTabId = chatActiveTabId;
  const streamingOnOldTab = chatStreamTabId === oldTabId && chatSending;

  if (streamingOnOldTab) {
    // Detach stream: move DOM children to a detached container so stream
    // writes continue there instead of polluting the new tab's view.
    chatDetachedContainer = document.createElement('div');
    while (chatMessages.firstChild) {
      chatDetachedContainer.appendChild(chatMessages.firstChild);
    }
    // Save snapshot from detached content (stream DOM refs remain valid).
    chatTabDOMCache[oldTabId] = {
      html: chatDetachedContainer.innerHTML,
      scrollTop: chatMessages.scrollTop,
      sending: true,
      jobId: chatJobId,
      eventOffset: chatEventOffset,
      historyLoaded: chatHistoryLoaded,
    };
    // Do NOT reset stream DOM refs — they still point to nodes inside
    // chatDetachedContainer and the running chatProcessStream needs them.
  } else {
    chatSnapshotTab();
  }

  chatActiveTabId = tabId;

  // Check if we're switching TO the tab that owns the running stream.
  const switchingToStreamTab = chatStreamTabId === tabId && chatDetachedContainer;

  if (switchingToStreamTab) {
    // Reattach: move detached stream content back into visible chatMessages.
    chatMessages.innerHTML = '';
    while (chatDetachedContainer.firstChild) {
      chatMessages.appendChild(chatDetachedContainer.firstChild);
    }
    chatDetachedContainer = null;
    chatScrollBottom();
    // Stream DOM refs still point to the now re-attached nodes — no reset needed.
    // Restore per-tab state that was NOT stream-related.
    const cached = chatTabDOMCache[tabId];
    if (cached) {
      chatHistoryLoaded = cached.historyLoaded || false;
    }
  } else {
    // Normal restore (either no stream, or switching away from streaming tab).
    chatRestoreTab(tabId);
  }

  chatRenderTabs();
  chatSaveTabs();
  // Clear badge on the tab we're switching to.
  chatTabUnread.delete(tabId);
  chatRenderTabs();
  // Clear status if the active tab is not the streaming tab.
  if (!chatStreamTabId || chatStreamTabId !== tabId) {
    chatStatus.innerHTML = '';
  }
  // Load history if not yet loaded for this tab.
  const tab = chatTabList.find(t => t.id === tabId);
  if (tab && !chatHistoryLoaded) {
    chatLoadHistory();
  }
  // Check for active job in this conversation.
  if (!chatSending) {
    chatCheckActiveJob();
  }
  chatSetStopMode(chatSending);
  chatInput.focus();
  requestAnimationFrame(() => chatScrollBottom());
}

function chatCreateTab(convId, title) {
  const id = chatGenerateTabId();
  const tab = { id, convId: convId || '', title: title || 'New chat' };
  chatTabList.push(tab);
  chatRenderTabs();
  chatSaveTabs();
  return tab;
}

function chatCloseTab(tabId) {
  if (chatTabList.length <= 1) return; // keep at least one
  // Don't close a tab that has an active stream.
  if (chatStreamTabId === tabId && chatSending) return;
  if (!confirm('Close this conversation tab?')) return;
  const idx = chatTabList.findIndex(t => t.id === tabId);
  if (idx < 0) return;
  chatTabList.splice(idx, 1);
  delete chatTabDOMCache[tabId];
  if (chatActiveTabId === tabId) {
    // Switch to nearest tab.
    const newIdx = Math.min(idx, chatTabList.length - 1);
    chatActiveTabId = chatTabList[newIdx].id;
    chatRestoreTab(chatActiveTabId);
    if (!chatHistoryLoaded) chatLoadHistory();
  }
  chatRenderTabs();
  chatSaveTabs();
}

// Reset the current tab: clear messages, create new backend session, keep same tab.
async function chatResetCurrentTab() {
  try {
    const res = await api('/api/chat', { method: 'DELETE' });
    const convId = res.conv_id || '';
    // Update the current tab's convId instead of creating a new one.
    const tab = chatTabList.find(t => t.id === chatActiveTabId);
    if (tab) {
      tab.convId = convId;
      tab.title = 'New chat';
    }
    chatMessages.innerHTML = '';
    chatSending = false;
    chatJobId = null;
    chatEventOffset = 0;
    chatHistoryLoaded = true;
    chatRenderTabs();
    chatSaveTabs();
    chatClearStatus();
    chatInput.focus();
  } catch {
    toast('Failed to reset chat', 'error');
  }
}

async function chatNewTab() {
  // Create a new session on the backend.
  try {
    const res = await api('/api/chat', { method: 'DELETE' });
    const convId = res.conv_id || '';
    chatSnapshotTab();
    const tab = chatCreateTab(convId, 'New chat');
    chatActiveTabId = tab.id;
    chatMessages.innerHTML = '';
    chatSending = false;
    chatJobId = null;
    chatEventOffset = 0;
    chatHistoryLoaded = true; // new session = empty
    chatRenderTabs();
    chatSaveTabs();
    chatClearStatus();
    chatInput.focus();
  } catch {
    toast('Failed to create new chat', 'error');
  }
}

// Update tab title from the first user message.
function chatUpdateTabTitle(text) {
  const tab = chatTabList.find(t => t.id === chatActiveTabId);
  if (tab && tab.title === 'New chat' && text) {
    tab.title = text.length > 40 ? text.slice(0, 40) + '...' : text;
    chatRenderTabs();
    chatSaveTabs();
  }
}

// Get the conv_id for the current active tab.
function chatActiveConvId() {
  const tab = chatTabList.find(t => t.id === chatActiveTabId);
  return (tab && tab.convId) || '';
}

// Initialize tabs on load.
function chatInitTabs() {
  chatLoadTabs();
  if (!chatTabList.length) {
    // First time -create default tab.
    const tab = chatCreateTab('', 'New chat');
    chatActiveTabId = tab.id;
  }
  // Ensure active tab exists.
  if (!chatTabList.find(t => t.id === chatActiveTabId)) {
    chatActiveTabId = chatTabList[0].id;
  }
  chatRenderTabs();
}
chatInitTabs();
if (window.lucide) lucide.createIcons();

document.getElementById('chatTabAdd').addEventListener('click', chatNewTab);

// --- Chat ---
let chatHistoryLoaded = false;
let chatSending = false;
let chatMessageQueue = []; // { id, text, files[], el } — pending messages waiting to be sent
let chatJobId = null;
let chatEventOffset = 0;
let chatReconnectTimer = null;
const chatMessages = document.getElementById('chatMessages');
const chatInput = document.getElementById('chatInput');
const chatSendBtn = document.getElementById('chatSendBtn');
const chatStatus = document.getElementById('chatStatus');
const chatMediaPreview = document.getElementById('chatMediaPreview');
const chatAttachBtn = document.getElementById('chatAttachBtn');
const chatFileInput = document.getElementById('chatFileInput');
const chatDropOverlay = document.getElementById('chatDropOverlay');
let chatPendingFiles = [];
let chatDragCounter = 0;

// Stop/Send button toggle
function chatSetStopMode(active) {
  if (active) {
    chatSendBtn.innerHTML = '<i data-lucide="square"></i>';
    chatSendBtn.title = 'Stop';
    chatSendBtn.classList.add('chat-stop-mode');
    chatSendBtn.disabled = false;
  } else {
    chatSendBtn.innerHTML = '<i data-lucide="send"></i>';
    chatSendBtn.title = 'Send';
    chatSendBtn.classList.remove('chat-stop-mode');
    chatSendBtn.disabled = false;
  }
  if (window.lucide) lucide.createIcons();
}
function chatStopJob() {
  const convId = chatActiveConvId();
  const url = '/api/chat/job' + (convId ? '?conv_id=' + encodeURIComponent(convId) : '');
  fetch(url, { method: 'DELETE', credentials: 'same-origin' })
    .then(r => r.json())
    .then(() => {
      chatAppendBubble('assistant', 'Request cancelled.', { tier: 'system' });
      chatScrollBottom();
      if (chatSending) chatFinishSend();
    })
    .catch(() => {
      chatAppendBubble('assistant', 'Failed to cancel.', { tier: 'system' });
      chatScrollBottom();
    });
}

// --- Chat media: attach button ---
chatAttachBtn.addEventListener('click', () => chatFileInput.click());
chatFileInput.addEventListener('change', () => {
  if (chatFileInput.files.length) {
    chatAddFiles(Array.from(chatFileInput.files));
    chatFileInput.value = '';
  }
});

// --- Chat media: drag and drop ---
const chatView = document.getElementById('chatView');
chatView.addEventListener('dragenter', (e) => {
  e.preventDefault();
  chatDragCounter++;
  if (chatDragCounter === 1) chatDropOverlay.classList.add('active');
});
chatView.addEventListener('dragover', (e) => e.preventDefault());
chatView.addEventListener('dragleave', (e) => {
  e.preventDefault();
  chatDragCounter--;
  if (chatDragCounter <= 0) { chatDragCounter = 0; chatDropOverlay.classList.remove('active'); }
});
chatView.addEventListener('drop', (e) => {
  e.preventDefault();
  chatDragCounter = 0;
  chatDropOverlay.classList.remove('active');
  if (e.dataTransfer.files.length) chatAddFiles(Array.from(e.dataTransfer.files));
});

// --- Chat media: clipboard paste ---
chatInput.addEventListener('paste', (e) => {
  const files = [];
  for (const item of (e.clipboardData || {}).items || []) {
    if (item.kind === 'file') files.push(item.getAsFile());
  }
  if (files.length) {
    e.preventDefault();
    chatAddFiles(files);
  }
});

// --- Chat media: pending files management ---
function chatAddFiles(files) {
  for (const f of files) {
    // Generate thumbnail for images, null for others.
    const entry = { file: f, url: null };
    if (f.type.startsWith('image/')) {
      entry.url = URL.createObjectURL(f);
    }
    chatPendingFiles.push(entry);
  }
  chatRenderPendingFiles();
}

function chatRemoveFile(idx) {
  const entry = chatPendingFiles[idx];
  if (entry.url) URL.revokeObjectURL(entry.url);
  chatPendingFiles.splice(idx, 1);
  chatRenderPendingFiles();
}

function chatRenderPendingFiles() {
  chatMediaPreview.innerHTML = '';
  if (!chatPendingFiles.length) {
    chatMediaPreview.classList.remove('has-files');
    return;
  }
  chatMediaPreview.classList.add('has-files');
  chatPendingFiles.forEach((entry, i) => {
    const thumb = document.createElement('div');
    thumb.className = 'chat-media-thumb' + (entry.url ? ' has-image' : '');
    if (entry.url) {
      const img = document.createElement('img');
      img.src = entry.url;
      thumb.appendChild(img);
    } else {
      const icon = document.createElement('div');
      icon.className = 'file-icon';
      icon.textContent = '\uD83D\uDCC4';
      thumb.appendChild(icon);
    }
    const name = document.createElement('span');
    name.className = 'name';
    name.textContent = entry.file.name;
    thumb.appendChild(name);
    const rm = document.createElement('button');
    rm.className = 'remove';
    rm.textContent = '\u00d7';
    rm.onclick = () => chatRemoveFile(i);
    thumb.appendChild(rm);
    chatMediaPreview.appendChild(thumb);
  });
}

// Upload pending files and return array of upload_ids.
async function chatUploadPendingFiles() {
  const ids = [];
  for (const entry of chatPendingFiles) {
    const form = new FormData();
    form.append('file', entry.file);
    form.append('type', entry.file.type.startsWith('image/') ? 'photo' : 'document');
    const res = await fetch('/api/chat/upload', { method: 'POST', credentials: 'same-origin', body: form });
    if (!res.ok) throw new Error('Upload failed: ' + entry.file.name);
    const data = await res.json();
    ids.push(data.upload_id);
  }
  // Clear pending list and hide preview bar. Don't revoke blob URLs yet —
  // they're reused as _localUrl in the user bubble for instant display.
  chatPendingFiles = [];
  chatRenderPendingFiles();
  return ids;
}

// Render media refs inside a chat bubble (for history & live messages).
function chatRenderMediaInBubble(bubble, mediaRefs) {
  if (!mediaRefs || !mediaRefs.length) return;
  const container = document.createElement('div');
  container.className = 'chat-bubble-media';
  mediaRefs.forEach(ref => {
    if (ref.mime_type && ref.mime_type.startsWith('image/')) {
      const img = document.createElement('img');
      const serverUrl = '/api/chat/media/' + ref.upload_id;
      img.src = ref._localUrl || serverUrl;
      img.alt = ref.file_name || 'image';
      img.title = ref.file_name || '';
      img.onerror = () => { img.style.display = 'none'; };
      img.addEventListener('click', () => window.open(serverUrl, '_blank'));
      container.appendChild(img);
    } else {
      const badge = document.createElement('span');
      badge.className = 'file-badge';
      badge.textContent = ref.file_name || 'file';
      container.appendChild(badge);
    }
  });
  bubble.prepend(container);
}

function chatLoadHistory() {
  if (chatHistoryLoaded) return;
  chatHistoryLoaded = true;
  const convId = chatActiveConvId();
  const url = '/api/chat?limit=50' + (convId ? '&conv_id=' + encodeURIComponent(convId) : '');
  fetch(url, { credentials: 'same-origin' })
    .then(r => {
      if (r.status === 401) { toast('Session expired', 'error'); throw new Error('401'); }
      return r.json();
    })
    .then(msgs => {
      if (!msgs || !msgs.length) return;
      chatMessages.innerHTML = '';
      msgs.forEach(m => chatAppendBubble(m.role, m.text, m));
      chatScrollBottom();
    })
    .then(() => { if (!chatStreamTabId) chatCheckActiveJob(); })
    .catch(() => {});
}

// Check for in-flight background job on page load / reconnect.
async function chatCheckActiveJob() {
  // If a stream is already running (on this tab or detached on another),
  // skip — reconnecting would duplicate events.
  if (chatStreamTabId) return;
  try {
    const convId = chatActiveConvId();
    const url = '/api/chat/job' + (convId ? '?conv_id=' + encodeURIComponent(convId) : '');
    const res = await fetch(url, { credentials: 'same-origin' });
    const data = await res.json();
    if (data.active) {
      chatJobId = data.job_id;
      chatEventOffset = 0;
      chatSending = true;
      chatStreamTabId = chatActiveTabId;
      chatDetachedContainer = null;
      chatSetStopMode(true);
      chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Reconnecting...');
      await chatStreamFromJob(chatJobId, 0);
      chatFinishSend();
    }
  } catch {}
}

// Stream events from a background job, with auto-reconnect on failure.
async function chatStreamFromJob(jobId, offset) {
  const url = '/api/chat/job?stream=' + encodeURIComponent(jobId) + '&offset=' + offset;
  try {
    const res = await fetch(url, { credentials: 'same-origin' });
    if (!res.ok) {
      // Job gone (server restart, expired) -clean up.
      chatJobId = null;
      return;
    }
    await chatProcessStream(res);
  } catch (e) {
    // Network error -auto-reconnect after delay.
    if (chatJobId) {
      await new Promise(r => setTimeout(r, 2000));
      if (chatJobId) return chatStreamFromJob(chatJobId, chatEventOffset);
    }
  }
}

const QUICK_REACTIONS = ['👍', '❤', '🔥', '😁', '🤔', '👎'];

// Legacy state vars removed -see chatThinkingEl, chatCurrentToolBlock, chatAgentTracker below.

// --- Thinking blocks (legacy-style <details> disclosures) ---

function chatNewThinkingBlock() {
  const det = document.createElement('details');
  det.className = 'chat-thinking-block';
  det.open = false;
  const summary = document.createElement('summary');
  summary.className = 'chat-thinking-summary';
  summary.innerHTML = '<i data-lucide="brain" class="chat-block-icon"></i> Thinking...';
  const content = document.createElement('div');
  content.className = 'chat-thinking-content';
  det.appendChild(summary);
  det.appendChild(content);
  chatStreamTarget().appendChild(det);
  chatThinkingEl = { det: det, summary: summary, content: content, text: '' };
  chatNeedNewBubble = true;
  if (window.lucide) lucide.createIcons({ nodes: [summary] });
  chatScrollBottom();
  return chatThinkingEl;
}

function chatAppendThinkingText(text) {
  if (!chatThinkingEl) chatNewThinkingBlock();
  chatThinkingEl.text += text;
  chatThinkingEl.content.textContent = chatThinkingEl.text;
  var preview = chatThinkingEl.text.slice(0, 100).replace(/\n/g, ' ');
  var iconHtml = chatThinkingEl.summary.querySelector('.chat-block-icon') ? chatThinkingEl.summary.querySelector('.chat-block-icon').outerHTML : '';
  chatThinkingEl.summary.innerHTML = iconHtml + ' ' + esc(preview) + (chatThinkingEl.text.length > 100 ? '…' : '');
  chatThinkingEl.content.scrollTop = chatThinkingEl.content.scrollHeight;
  chatScrollBottom();
}

// --- Tool blocks (inline bubbles with name + input + result) ---

function chatNewToolBlock(name) {
  // Close previous thinking block when a tool starts
  if (chatThinkingEl && chatThinkingEl.det.open) {
    chatThinkingEl.det.open = false;
  }
  var det = document.createElement('details');
  det.className = 'chat-tool-block';
  var summary = document.createElement('summary');
  summary.className = 'chat-tool-summary';
  summary.innerHTML = '<i data-lucide="wrench" class="chat-block-icon"></i> <strong>' + esc(name) + '</strong>';
  det.appendChild(summary);
  var inputEl = document.createElement('div');
  inputEl.className = 'chat-tool-input';
  det.appendChild(inputEl);
  var resultEl = document.createElement('div');
  resultEl.className = 'chat-tool-result-inline';
  det.appendChild(resultEl);
  chatStreamTarget().appendChild(det);
  chatCurrentToolBlock = det;
  chatCurrentToolInput = '';
  chatNeedNewBubble = true;
  if (window.lucide) lucide.createIcons({ nodes: [summary] });
  chatScrollBottom();
  return det;
}

// --- Agent tracker (compact group for orchestrator phase events) ---

function chatGetOrCreateAgentTracker() {
  if (chatAgentTracker) return chatAgentTracker;
  chatAgentTracker = document.createElement('div');
  chatAgentTracker.className = 'chat-agent-tracker';
  var toggle = document.createElement('button');
  toggle.className = 'chat-agent-toggle';
  toggle.innerHTML = '<span class="chevron">&#9654;</span> <span class="agent-summary">Agent working...</span>';
  toggle.addEventListener('click', function() {
    chatAgentTracker.classList.toggle('open');
  });
  chatAgentTrackerBody = document.createElement('div');
  chatAgentTrackerBody.className = 'chat-agent-body';
  chatAgentTracker.appendChild(toggle);
  chatAgentTracker.appendChild(chatAgentTrackerBody);
  chatAgentTracker.classList.add('open');
  chatStreamTarget().appendChild(chatAgentTracker);
  chatAgentStepCount = 0;
  return chatAgentTracker;
}

function chatAppendAgentStep(type, label) {
  chatGetOrCreateAgentTracker();
  var el = document.createElement('div');
  el.className = 'chat-step ' + type;
  var icons = {
    task_started: '📌', agent_start: '🤖', agent_done: '✅',
    agent_thinking: '🧠', agent_tool: '🔧', planning: '📋', synthesizing: '🔄'
  };
  var icon = icons[type] || '▸';
  el.innerHTML = '<span class="chat-step-icon">' + icon + '</span> <span class="chat-step-label">' + esc(label) + '</span>';
  chatAgentTrackerBody.appendChild(el);
  chatAgentStepCount++;
  chatScrollBottom();
}

function chatUpdateAgentSummary() {
  if (!chatAgentTracker) return;
  var summary = chatAgentTracker.querySelector('.agent-summary');
  if (!summary) return;
  var starts = chatAgentTrackerBody.querySelectorAll('.chat-step.agent_start').length;
  var dones = chatAgentTrackerBody.querySelectorAll('.chat-step.agent_done').length;
  var running = starts - dones;
  var parts = [];
  if (running > 0) parts.push(running + ' agent' + (running > 1 ? 's' : '') + ' running');
  if (dones > 0) parts.push(dones + ' done');
  summary.textContent = parts.join(' · ') || 'Agent working...';
}

// Update bubble text content without clobbering meta/reactions elements.
function chatUpdateBubbleContent(bubble, text) {
  let contentEl = bubble.querySelector('.chat-bubble-content');
  if (!contentEl) {
    contentEl = document.createElement('div');
    contentEl.className = 'chat-bubble-content';
    bubble.insertBefore(contentEl, bubble.firstChild);
  }
  contentEl.innerHTML = chatRenderMd(text);
}

function chatAppendBubble(role, text, meta) {
  const bubble = document.createElement('div');
  bubble.className = 'chat-bubble ' + role;
  if (meta && meta.id) bubble.dataset.msgId = meta.id;
  // Render media attachments BEFORE text so images appear above the message.
  if (meta && meta.media && meta.media.length) {
    chatRenderMediaInBubble(bubble, meta.media);
  }
  if (text) {
    const textEl = document.createElement('div');
    textEl.innerHTML = chatRenderMd(text);
    bubble.appendChild(textEl);
  }

  const metaEl = document.createElement('div');
  metaEl.className = 'chat-bubble-meta';
  const ts = meta && meta.ts ? new Date(meta.ts).toLocaleTimeString() : new Date().toLocaleTimeString();
  let metaText = ts;
  if (role === 'assistant' && meta) {
    if (meta.tier) metaText += ' · ' + meta.tier;
    if (meta.model) metaText += ' · ' + meta.model;
  }
  metaEl.textContent = metaText;

  // Reactions container
  const reactionsEl = document.createElement('span');
  reactionsEl.className = 'chat-reactions';
  if (meta && meta.reactions && meta.reactions.length) {
    meta.reactions.forEach(r => {
      const span = document.createElement('span');
      span.className = 'chat-bubble-reaction';
      span.textContent = r.emoji;
      reactionsEl.appendChild(span);
    });
  }
  metaEl.appendChild(reactionsEl);

  bubble.appendChild(metaEl);

  // Action buttons for assistant bubbles
  if (role === 'assistant' && meta && meta.id) {
    const actionsWrap = document.createElement('div');
    actionsWrap.className = 'chat-bubble-actions';

    const reactBtn = document.createElement('button');
    reactBtn.className = 'chat-react-btn';
    reactBtn.textContent = '😊';
    reactBtn.title = 'React';
    reactBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      chatShowReactPicker(bubble, meta.id, reactBtn);
    });
    actionsWrap.appendChild(reactBtn);

    // "Send to agents" button — opens modal to configure and delegate.
    if (text && text.length > 10) {
      const agentBtn = document.createElement('button');
      agentBtn.className = 'chat-agent-btn';
      agentBtn.innerHTML = '<i data-lucide="users" style="width:14px;height:14px"></i>';
      agentBtn.title = 'Send to agents';
      agentBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        agentModalShow(text.substring(0, 2000));
      });
      actionsWrap.appendChild(agentBtn);
    }

    bubble.appendChild(actionsWrap);
  }

  chatMessages.appendChild(bubble);
  return bubble;
}

function chatShowReactPicker(bubble, msgId, anchor) {
  // Remove existing picker
  document.querySelectorAll('.chat-react-picker').forEach(p => p.remove());

  const picker = document.createElement('div');
  picker.className = 'chat-react-picker';
  QUICK_REACTIONS.forEach(emoji => {
    const btn = document.createElement('button');
    btn.textContent = emoji;
    btn.addEventListener('click', () => {
      picker.remove();
      chatSendReaction(bubble, msgId, emoji);
    });
    picker.appendChild(btn);
  });
  bubble.appendChild(picker);

  // Dismiss on outside click
  const dismiss = (e) => {
    if (!picker.contains(e.target) && e.target !== anchor) {
      picker.remove();
      document.removeEventListener('click', dismiss);
    }
  };
  setTimeout(() => document.addEventListener('click', dismiss), 0);
}

function chatSendReaction(bubble, msgId, emoji) {
  fetch('/api/chat/react', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ msg_id: msgId, emoji: emoji }),
  }).then(r => r.json()).then(result => {
    if (!result.ok) return;
    const reactionsEl = bubble.querySelector('.chat-reactions');
    if (reactionsEl) {
      const span = document.createElement('span');
      span.className = 'chat-bubble-reaction';
      span.textContent = emoji;
      reactionsEl.appendChild(span);
      // Mirror reaction from ALF
      if (result.mirror) {
        const mirrorSpan = document.createElement('span');
        mirrorSpan.className = 'chat-bubble-reaction';
        mirrorSpan.textContent = result.mirror;
        reactionsEl.appendChild(mirrorSpan);
      }
    }
  }).catch(() => {});
}

function chatScrollBottom() {
  // Only scroll if the stream is rendering on the visible tab.
  if (!chatDetachedContainer) {
    chatMessages.scrollTop = chatMessages.scrollHeight;
  }
}

function chatSetStatus(html) {
  // Only show status bar when the streaming tab is the active tab.
  if (chatStreamTabId && chatStreamTabId !== chatActiveTabId) return;
  chatStatus.innerHTML = html;
  // Status bar appearing shrinks chat-messages; re-scroll to keep bottom visible.
  requestAnimationFrame(() => { chatMessages.scrollTop = chatMessages.scrollHeight; });
}

function chatClearStatus() {
  chatStatus.innerHTML = '';
}

function chatRenderMd(text) {
  if (!text) return '';
  if (typeof marked !== 'undefined') {
    const raw = marked.parse(text, { breaks: true, gfm: true });
    return typeof DOMPurify !== 'undefined' ? DOMPurify.sanitize(raw) : raw;
  }
  // Fallback if marked.js not loaded.
  let html = esc(text);
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) =>
    '<pre><code>' + code + '</code></pre>'
  );
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  html = html.replace(/\n\n+/g, '</p><p>');
  html = '<p>' + html + '</p>';
  html = html.replace(/\n/g, '<br>');
  html = html.replace(/<p>\s*<pre>/g, '<pre>');
  html = html.replace(/<\/pre>\s*<\/p>/g, '</pre>');
  return html;
}

// Shared stream state for current response.
let chatAssistantBubble = null;
let chatFullText = '';
let chatDoneData = null;
let chatReaction = null;
let chatStreamingText = false; // true once we start receiving text_delta
let chatThinkingEl = null;        // { det, summary, content, text } current thinking block
let chatCurrentToolBlock = null;  // current tool block div
let chatCurrentToolInput = '';    // accumulated tool input JSON
let chatAgentTracker = null;      // agent events container
let chatAgentTrackerBody = null;
let chatAgentStepCount = 0;
let chatCurrentTier = '';         // tier name from routing
let chatNeedNewBubble = false;   // true when tool/thinking happened mid-stream → next text creates fresh bubble

// Tab-aware stream isolation: tracks which tab owns the running stream.
let chatStreamTabId = null;          // tab that initiated the current stream
let chatDetachedContainer = null;    // detached DOM container when stream tab is not active

// Returns the correct DOM container for stream output (chatMessages if active, detached if not).
function chatStreamTarget() {
  return chatDetachedContainer || chatMessages;
}

// Process an SSE stream response (shared by send and reconnect).
async function chatProcessStream(res) {
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    const chunk = decoder.decode(value, { stream: true });
    buffer += chunk;
    const parts = buffer.split('\n\n');
    buffer = parts.pop();

    for (const part of parts) {
      const lines = part.split('\n');
      let eventType = '';
      let eventData = '';
      for (const line of lines) {
        if (line.startsWith('event: ')) eventType = line.slice(7);
        else if (line.startsWith('data: ')) eventData = line.slice(6);
      }
      if (!eventType) continue;

      let data;
      try { data = JSON.parse(eventData); } catch { continue; }

      chatEventOffset++;

      switch (eventType) {
        case 'job':
          chatJobId = data.job_id;
          chatEventOffset = 0; // job event doesn't count as a buffered event
          break;
        case 'routed':
          chatCurrentTier = data.tier || '';
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Routed to <strong>' + esc(chatCurrentTier) + '</strong>');
          break;
        case 'thinking':
          if (data.text) {
            chatAppendThinkingText(data.text);
          } else {
            chatNewThinkingBlock();
            var tierTag = chatCurrentTier ? ' <span style="opacity:0.6;font-weight:normal">(' + esc(chatCurrentTier) + ')</span>' : '';
            chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Thinking...' + tierTag);
          }
          break;
        case 'task_started':
          chatAppendAgentStep('task_started', 'Task ' + (data.task_id || ''));
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Agent task started...');
          break;
        case 'tool_use':
          chatNewToolBlock(data.name || 'tool');
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Using ' + esc(data.name || 'tool') + '...');
          break;
        case 'tool_input':
          if (chatCurrentToolBlock) {
            chatCurrentToolInput += (data.chunk || '');
            var inputEl = chatCurrentToolBlock.querySelector('.chat-tool-input');
            if (inputEl) {
              var display = chatCurrentToolInput;
              try {
                var parsed = JSON.parse(chatCurrentToolInput);
                display = Object.entries(parsed).map(function(e) {
                  var vs = typeof e[1] === 'string' ? e[1] : JSON.stringify(e[1]);
                  return e[0] + ': ' + (vs.length > 120 ? vs.slice(0, 120) + '\u2026' : vs);
                }).join('\n');
                // Show key param preview in the summary line.
                var preview = parsed.command || parsed.file_path || parsed.pattern || parsed.path || '';
                if (preview) {
                  var summaryEl = chatCurrentToolBlock.querySelector('.chat-tool-summary');
                  if (summaryEl) {
                    var strongEl = summaryEl.querySelector('strong');
                    var toolName = strongEl ? strongEl.textContent : '';
                    var iconEl = summaryEl.querySelector('.chat-block-icon');
                    var iconHtml = iconEl ? iconEl.outerHTML : '';
                    var short = preview.length > 60 ? preview.slice(0, 60) + '\u2026' : preview;
                    summaryEl.innerHTML = iconHtml + ' <strong>' + esc(toolName) + '</strong> <span class="chat-tool-preview">' + esc(short) + '</span>';
                  }
                }
              } catch(ex) { /* still accumulating JSON */ }
              inputEl.textContent = display;
            }
          }
          break;
        case 'tool_result':
          if (chatCurrentToolBlock) {
            var resultEl = chatCurrentToolBlock.querySelector('.chat-tool-result-inline');
            if (resultEl) {
              var resultText = data.result || '';
              if (resultText.length > 300) resultText = resultText.slice(0, 300) + '…';
              resultEl.textContent = resultText;
            }
            chatCurrentToolBlock = null;
            chatCurrentToolInput = '';
          }
          break;
        case 'planning':
          chatAppendAgentStep('planning', data.detail || 'Planning...');
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Planning...');
          break;
        case 'agent_start':
          chatAppendAgentStep('agent_start', 'Agent: ' + (data.name || '?'));
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Agent ' + esc(data.name || '') + ' running...');
          break;
        case 'agent_thinking':
          chatAppendAgentStep('agent_thinking', (data.name || 'Agent') + ' thinking...');
          break;
        case 'agent_tool': {
          var agParts = (data.detail || '').split(':');
          var agentName = agParts[0] || 'Agent';
          var toolName = agParts.slice(1).join(':') || 'tool';
          chatAppendAgentStep('agent_tool', agentName + ' → ' + toolName);
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> ' + esc(agentName) + ' using ' + esc(toolName) + '...');
          break;
        }
        case 'agent_done':
          chatAppendAgentStep('agent_done', data.detail || 'Agent done');
          chatUpdateAgentSummary();
          break;
        case 'synthesizing':
          chatAppendAgentStep('synthesizing', 'Synthesizing results...');
          chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Synthesizing...');
          break;
        case 'text_delta':
          if (!chatStreamingText || chatNeedNewBubble) {
            chatStreamingText = true;
            chatNeedNewBubble = false;
            chatClearStatus();
            // Collapse thinking block when text starts arriving.
            if (chatThinkingEl && chatThinkingEl.det.open) chatThinkingEl.det.open = false;
            // Collapse agent tracker when text starts arriving.
            if (chatAgentTracker) chatAgentTracker.classList.remove('open');
            // Start a fresh bubble (new bubble per text section after tools/thinking)
            chatAssistantBubble = document.createElement('div');
            chatAssistantBubble.className = 'chat-bubble assistant';
            chatAssistantBubble.innerHTML = '';
            chatFullText = '';
            var metaEl = document.createElement('div');
            metaEl.className = 'chat-bubble-meta';
            var reactionsEl = document.createElement('span');
            reactionsEl.className = 'chat-reactions';
            metaEl.appendChild(reactionsEl);
            chatAssistantBubble.appendChild(metaEl);
            chatStreamTarget().appendChild(chatAssistantBubble);
          }
          chatFullText += (data.text || '');
          chatUpdateBubbleContent(chatAssistantBubble, chatFullText);
          chatScrollBottom();
          break;
        case 'text':
          // Final full text (fallback for non-streaming or final confirmation).
          chatFullText = data.text || chatFullText;
          if (!chatAssistantBubble) {
            // Create bubble in the stream target (may be detached).
            chatAssistantBubble = document.createElement('div');
            chatAssistantBubble.className = 'chat-bubble assistant';
            chatAssistantBubble.innerHTML = chatRenderMd(chatFullText);
            var tm = document.createElement('div');
            tm.className = 'chat-bubble-meta';
            tm.textContent = new Date().toLocaleTimeString();
            var tr = document.createElement('span');
            tr.className = 'chat-reactions';
            tm.appendChild(tr);
            chatAssistantBubble.appendChild(tm);
            chatStreamTarget().appendChild(chatAssistantBubble);
          } else {
            chatUpdateBubbleContent(chatAssistantBubble, chatFullText);
          }
          chatScrollBottom();
          break;
        case 'system': {
          var sysBubble = document.createElement('div');
          sysBubble.className = 'chat-bubble system';
          sysBubble.textContent = data.text || '';
          chatStreamTarget().appendChild(sysBubble);
          chatScrollBottom();
          break;
        }
        case 'reaction':
          chatReaction = data.emoji;
          break;
        case 'done':
          chatDoneData = data;
          chatJobId = null; // job complete
          break;
        case 'error':
          // Show error inline in chat bubble instead of disruptive toast.
          var errMsg = data.error || 'Chat error';
          console.error('[chat] stream error:', errMsg);
          if (!chatAssistantBubble) {
            chatAssistantBubble = document.createElement('div');
            chatAssistantBubble.className = 'chat-bubble assistant';
            var metaEl = document.createElement('div');
            metaEl.className = 'chat-bubble-meta';
            var reactionsEl = document.createElement('span');
            reactionsEl.className = 'chat-reactions';
            metaEl.appendChild(reactionsEl);
            chatAssistantBubble.appendChild(metaEl);
            chatStreamTarget().appendChild(chatAssistantBubble);
          }
          chatAssistantBubble.classList.add('chat-error');
          chatFullText += (chatFullText ? '\n\n' : '') + errMsg;
          chatUpdateBubbleContent(chatAssistantBubble, chatFullText);
          chatScrollBottom();
          chatJobId = null;
          break;
      }
    }
  }
}

// Finalize the assistant bubble after stream completes.
function chatFinalizeBubble() {
  if (chatAssistantBubble && chatDoneData) {
    chatAssistantBubble.dataset.msgId = chatDoneData.msg_id;
    const metaEl = chatAssistantBubble.querySelector('.chat-bubble-meta');
    if (metaEl) {
      const reactionsEl = metaEl.querySelector('.chat-reactions');
      let parts = [new Date().toLocaleTimeString()];
      if (chatDoneData.tier) parts.push(chatDoneData.tier);
      if (chatDoneData.model) parts.push(chatDoneData.model);
      if (chatDoneData.cost_usd) parts.push('$' + chatDoneData.cost_usd.toFixed(4));
      if (chatDoneData.duration_ms) {
        const secs = (chatDoneData.duration_ms / 1000).toFixed(1);
        parts.push(secs + 's');
      }
      if (chatDoneData.skills && chatDoneData.skills.length > 0) {
        parts.push(chatDoneData.skills.join(', '));
      }
      metaEl.textContent = parts.join(' · ');
      if (reactionsEl) {
        metaEl.appendChild(reactionsEl);
      } else {
        const newReactionsEl = document.createElement('span');
        newReactionsEl.className = 'chat-reactions';
        metaEl.appendChild(newReactionsEl);
      }
      if (chatReaction) {
        const rEl = metaEl.querySelector('.chat-reactions');
        const span = document.createElement('span');
        span.className = 'chat-bubble-reaction';
        span.textContent = chatReaction;
        if (rEl) rEl.appendChild(span);
      }
    }
    // Add action buttons (react + send to agents) in proper wrapper.
    // Capture references in locals — chatAssistantBubble and chatFullText are
    // reset to null/'' in chatFinishSend() right after this function returns.
    const msgId = chatDoneData.msg_id;
    const bubble = chatAssistantBubble;
    const fullText = chatFullText;
    if (msgId) {
      const actionsWrap = document.createElement('div');
      actionsWrap.className = 'chat-bubble-actions';

      const reactBtn = document.createElement('button');
      reactBtn.className = 'chat-react-btn';
      reactBtn.textContent = '\u{1F60A}';
      reactBtn.title = 'React';
      reactBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        chatShowReactPicker(bubble, msgId, reactBtn);
      });
      actionsWrap.appendChild(reactBtn);

      if (fullText && fullText.length > 10) {
        const agentBtn = document.createElement('button');
        agentBtn.className = 'chat-agent-btn';
        agentBtn.innerHTML = '<i data-lucide="users" style="width:14px;height:14px"></i>';
        agentBtn.title = 'Send to agents';
        agentBtn.addEventListener('click', (e) => {
          e.stopPropagation();
          agentModalShow(fullText.substring(0, 2000));
        });
        actionsWrap.appendChild(agentBtn);
      }

      bubble.appendChild(actionsWrap);
      if (typeof lucide !== 'undefined') lucide.createIcons({ nodes: [actionsWrap] });
    }
  }
}

function chatFinishSend() {
  chatFinalizeBubble();
  chatShowBadge();
  notify('ALF', chatFullText || 'Response ready');
  chatClearStatus();
  chatSending = false;
  chatSetStopMode(false);
  chatInput.focus();

  // If the stream finished while we were on a different tab, mark unread.
  if (chatStreamTabId && chatStreamTabId !== chatActiveTabId) {
    chatTabUnread.add(chatStreamTabId);
    chatRenderTabs();
  }

  // If the stream finished while we were on a different tab, persist
  // the detached container's content into the originating tab's DOM cache.
  if (chatDetachedContainer && chatStreamTabId) {
    chatTabDOMCache[chatStreamTabId] = {
      html: chatDetachedContainer.innerHTML,
      scrollTop: 0,
      sending: false,
      jobId: null,
      eventOffset: 0,
      historyLoaded: chatTabDOMCache[chatStreamTabId]
        ? chatTabDOMCache[chatStreamTabId].historyLoaded
        : false,
    };
    chatDetachedContainer = null;
  } else {
    // Stream finished on the active tab — snapshot normally.
    chatSnapshotTab();
  }

  chatStreamTabId = null;
  chatAssistantBubble = null;
  chatFullText = '';
  chatDoneData = null;
  chatReaction = null;
  chatJobId = null;
  chatStreamingText = false;
  chatThinkingEl = null;
  chatCurrentToolBlock = null;
  chatCurrentToolInput = '';
  chatAgentTracker = null;
  chatAgentTrackerBody = null;
  chatAgentStepCount = 0;
  chatCurrentTier = '';
  chatNeedNewBubble = false;

  // Process next queued message if any.
  chatSendNextQueued();
}

// --- Message Queue ---
let chatQueueIdCounter = 0;

function chatQueueMessage(text, files) {
  const id = 'q-' + (++chatQueueIdCounter);
  const el = document.createElement('div');
  el.className = 'chat-bubble user chat-queued';
  el.dataset.queueId = id;

  const textEl = document.createElement('div');
  textEl.textContent = text || (files.length ? files.length + ' file(s)' : '');
  el.appendChild(textEl);

  const actions = document.createElement('div');
  actions.className = 'chat-queued-actions';

  const label = document.createElement('span');
  label.className = 'chat-queued-label';
  label.textContent = 'Queued';
  actions.appendChild(label);

  const cancelBtn = document.createElement('button');
  cancelBtn.className = 'chat-queued-cancel';
  cancelBtn.title = 'Cancel';
  cancelBtn.innerHTML = '<i data-lucide="x" style="width:14px;height:14px"></i>';
  cancelBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    chatDequeueMessage(id);
  });
  actions.appendChild(cancelBtn);
  el.appendChild(actions);

  document.getElementById('chatQueue').appendChild(el);
  if (typeof lucide !== 'undefined') lucide.createIcons({ nodes: [el] });
  chatScrollBottom();

  chatMessageQueue.push({ id, text, files, el, tabId: chatActiveTabId });
}

function chatDequeueMessage(id) {
  const idx = chatMessageQueue.findIndex(m => m.id === id);
  if (idx === -1) return;
  const item = chatMessageQueue.splice(idx, 1)[0];
  if (item.el && item.el.parentNode) item.el.remove();
}

async function chatSendNextQueued() {
  if (chatSending || chatMessageQueue.length === 0) return;

  const next = chatMessageQueue.shift();
  if (next.el && next.el.parentNode) next.el.remove();

  // Switch to the tab where the message was queued, if different.
  if (next.tabId && next.tabId !== chatActiveTabId) {
    chatSwitchTab(next.tabId);
  }

  // Inject into input and trigger send.
  chatInput.value = next.text || '';
  chatPendingFiles = next.files || [];
  if (chatPendingFiles.length) chatRenderPendingFiles();
  await chatSend();
}

async function chatSend() {
  const text = chatInput.value.trim();
  const hasFiles = chatPendingFiles.length > 0;
  if (!text && !hasFiles) return;

  // If already sending, queue the message instead of blocking.
  if (chatSending) {
    chatQueueMessage(text, hasFiles ? [...chatPendingFiles] : []);
    chatInput.value = '';
    chatInput.style.height = '';
    if (hasFiles) { chatPendingFiles = []; chatRenderPendingFiles(); }
    return;
  }

  // Handle /bash command locally.
  if (text.startsWith('/bash ')) {
    const cmd = text.substring(6).trim();
    if (!cmd) return;
    chatInput.value = '';
    chatInput.style.height = '';
    chatDismissCommands();
    chatAppendBubble('user', text, {});
    chatScrollBottom();
    chatSetStatus('Executing...');
    chatSending = true;
    chatSetStopMode(true);
    try {
      const res = await fetch('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ command: cmd }),
      });
      const data = await res.json();
      let output = '';
      if (data.output) output = '```\n' + data.output + '\n```';
      if (data.error && data.exit_code !== 0) output += (output ? '\n' : '') + '**Exit ' + data.exit_code + ':** ' + data.error;
      if (!output) output = '*Command completed (no output)*';
      chatAppendBubble('assistant', output, { tier: 'direct' });
    } catch (e) {
      chatAppendBubble('assistant', '**Error:** ' + e.message, { tier: 'system' });
    }
    chatClearStatus();
    chatSending = false;
    chatSetStopMode(false);
    chatScrollBottom();
    return;
  }

  // Handle other slash commands.
  if (text.startsWith('/') && !text.startsWith('//')) {
    const cmdName = text.split(' ')[0];
    const fullCmd = text.trim();
    // Check for subcommands (e.g. "/skills clear").
    if (CHAT_COMMANDS.some(c => c.name === fullCmd)) {
      chatDismissCommands();
      chatExecCommand(fullCmd);
      return;
    }
    const isForceCmd = CHAT_COMMANDS.some(c => c.dynamic && c.name === cmdName);
    if (!isForceCmd && CHAT_COMMANDS.some(c => c.name === cmdName)) {
      chatDismissCommands();
      chatExecCommand(cmdName);
      return;
    }
  }

  chatSending = true;
  chatSetStopMode(true);
  chatInput.value = '';
  chatInput.style.height = '';

  // Reset stream state.
  chatStreamTabId = chatActiveTabId;
  chatDetachedContainer = null;
  chatAssistantBubble = null;
  chatFullText = '';
  chatDoneData = null;
  chatReaction = null;
  chatJobId = null;
  chatEventOffset = 0;
  chatStreamingText = false;
  chatThinkingEl = null;
  chatCurrentToolBlock = null;
  chatCurrentToolInput = '';
  chatAgentTracker = null;
  chatAgentTrackerBody = null;
  chatAgentStepCount = 0;

  // Upload pending files first.
  let mediaIds = [];
  let pendingBlobUrls = [];
  if (hasFiles) {
    // Save blob URLs before upload (upload clears chatPendingFiles).
    pendingBlobUrls = chatPendingFiles.map(e => ({ url: e.url, mime: e.file.type, name: e.file.name }));
    chatSetStatus('Uploading files...');
    try {
      mediaIds = await chatUploadPendingFiles();
    } catch (e) {
      toast('Upload failed: ' + e.message, 'error');
      chatFinishSend();
      return;
    }
  }

  // Build local media refs with blob URLs for instant thumbnail display.
  const localMedia = pendingBlobUrls.map((p, i) => ({
    upload_id: mediaIds[i],
    mime_type: p.mime || '',
    file_name: p.name || '',
    _localUrl: p.url,
  }));
  chatAppendBubble('user', text, { media: localMedia.length ? localMedia : undefined });
  chatScrollBottom();
  chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Thinking...');

  try {
    const payload = { message: text, conv_id: chatActiveConvId() };
    if (mediaIds.length) payload.media_ids = mediaIds;
    chatUpdateTabTitle(text);
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify(payload),
    });

    if (res.status === 401) {
      toast('Session expired', 'error');
      chatFinishSend();
      return;
    }
    if (res.status === 409) {
      toast('A request is already running', 'error');
      chatFinishSend();
      return;
    }

    await chatProcessStream(res);
  } catch (e) {
    // Connection lost -try to reconnect to background job.
    if (chatJobId) {
      chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Reconnecting...');
      await chatStreamFromJob(chatJobId, chatEventOffset);
    } else {
      toast('Failed to send message', 'error');
    }
  }

  chatFinishSend();
}

// Auto-reconnect when tab becomes visible again.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') {
    // Scroll to bottom — scrollHeight was 0 while the browser tab was hidden.
    requestAnimationFrame(() => chatScrollBottom());
    if (!chatSending) chatCheckActiveJob();
  }
});

// --- Chat Commands ---
const CHAT_COMMANDS = [
  { name: '/clear', description: 'Clear current chat messages', icon: 'trash-2' },
  { name: '/new', description: 'Open a new conversation tab', icon: 'plus' },
  { name: '/stop', description: 'Cancel the running request', icon: 'square' },
  { name: '/start', description: 'Re-run onboarding', icon: 'play' },
  { name: '/restart', description: 'Restart ALF daemon', icon: 'power' },
  { name: '/bash', description: 'Execute a bash command', icon: 'terminal', dynamic: true },
  { name: '/skills', description: 'List active skills in this session', icon: 'sparkles' },
  { name: '/skills clear', description: 'Remove all active skills from session', icon: 'sparkles' },
  { name: '/help', description: 'Show available commands', icon: 'help-circle' },
];

// Load dynamic force-command tiers into CHAT_COMMANDS.
(function loadForceCommands() {
  api('/api/memory/tiers').then(tiers => {
    tiers.forEach(t => {
      if (t.force_command) {
        CHAT_COMMANDS.push({
          name: '/' + t.name,
          description: 'Force ' + t.model + ' tier (' + t.name + ')',
          icon: 'zap',
          dynamic: true,
        });
      }
    });
  }).catch(() => {});
})();

let cmdPickerEl = null;
let cmdSelectedIdx = 0;

function chatShowCommands(filter) {
  chatDismissCommands();
  const matches = CHAT_COMMANDS.filter(c => c.name.startsWith(filter));
  if (!matches.length) return;

  cmdPickerEl = document.createElement('div');
  cmdPickerEl.className = 'chat-cmd-picker';
  cmdSelectedIdx = 0;

  matches.forEach((cmd, i) => {
    const item = document.createElement('div');
    item.className = 'chat-cmd-item' + (i === 0 ? ' selected' : '');
    item.innerHTML = '<span class="chat-cmd-name">' + esc(cmd.name) + '</span><span class="chat-cmd-desc">' + esc(cmd.description) + '</span>';
    item.addEventListener('click', () => {
      chatDismissCommands();
      if (cmd.dynamic) {
        // Dynamic force commands: fill input with command prefix, let user type message
        chatInput.value = cmd.name + ' ';
        chatInput.focus();
        return;
      }
      chatExecCommand(cmd.name);
    });
    item.addEventListener('mouseenter', () => {
      cmdPickerEl.querySelectorAll('.chat-cmd-item').forEach(el => el.classList.remove('selected'));
      item.classList.add('selected');
      cmdSelectedIdx = i;
    });
    cmdPickerEl.appendChild(item);
  });

  document.getElementById('chatView').insertBefore(cmdPickerEl, document.querySelector('.chat-input-bar'));
}

function chatDismissCommands() {
  if (cmdPickerEl) {
    cmdPickerEl.remove();
    cmdPickerEl = null;
  }
}

function chatExecCommand(cmd) {
  chatInput.value = '';
  chatInput.style.height = '';

  switch (cmd) {
    case '/help':
      chatAppendBubble('assistant', 'Available commands:\n' + CHAT_COMMANDS.map(c => '**' + c.name + '** -' + c.description).join('\n'), { tier: 'system' });
      chatScrollBottom();
      break;
    case '/clear':
    case '/new':
      chatResetCurrentTab();
      break;
    case '/start':
      fetch('/api/chat?onboard=1', { method: 'DELETE', credentials: 'same-origin' })
        .then(r => r.json())
        .then(r => {
          if (r.ok) {
            const convId = r.conv_id || '';
            chatSnapshotTab();
            const tab = chatCreateTab(convId, 'Onboarding');
            chatActiveTabId = tab.id;
            chatMessages.innerHTML = '';
            chatSending = false;
            chatJobId = null;
            chatEventOffset = 0;
            chatHistoryLoaded = true;
            chatRenderTabs();
            chatSaveTabs();
            chatInput.value = 'hello';
            chatSend();
          } else {
            chatAppendBubble('assistant', 'Failed.', { tier: 'system' });
            chatScrollBottom();
          }
        })
        .catch(() => { chatAppendBubble('assistant', 'Failed.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/skills':
      api('/api/chat/skills').then(r => {
        const skills = r.skills || [];
        if (skills.length === 0) {
          chatAppendBubble('assistant', 'No skills active in this session.\n\nUse **/skills clear** to reset after loading skills.', { tier: 'system' });
        } else {
          chatAppendBubble('assistant', '**Active skills:**\n' + skills.map(s => '- ' + s).join('\n') + '\n\nUse **/skills clear** to reset.', { tier: 'system' });
        }
        chatScrollBottom();
      }).catch(() => { chatAppendBubble('assistant', 'Failed to fetch skills.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/skills clear':
      api('/api/chat/skills', { method: 'DELETE' }).then(() => {
        chatAppendBubble('assistant', 'Active skills cleared from session.', { tier: 'system' });
        chatScrollBottom();
      }).catch(() => { chatAppendBubble('assistant', 'Failed to clear skills.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/stop':
      chatStopJob();
      break;
    case '/restart':
      if (!confirm('Restart ALF daemon?')) return;
      fetch('/api/restart', { method: 'POST', credentials: 'same-origin' }).catch(() => {});
      chatAppendBubble('assistant', 'Restarting...', { tier: 'system' }); chatScrollBottom();
      waitForDaemonAndReload(function(msg) {
        chatAppendBubble('assistant', msg, { tier: 'system' }); chatScrollBottom();
      });
      break;
  }
}

function chatAppendSystemMessage(text) {
  const el = document.createElement('div');
  el.className = 'chat-system-msg';
  // Minimal markdown: **bold** and `code`.
  el.innerHTML = esc(text)
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/`(.+?)`/g, '<code>$1</code>');
  chatMessages.appendChild(el);
}

chatSendBtn.addEventListener('click', () => {
  if (chatSendBtn.classList.contains('chat-stop-mode')) {
    chatStopJob();
  } else {
    chatSend();
  }
});
chatInput.addEventListener('keydown', (e) => {
  // Command picker navigation
  if (cmdPickerEl) {
    const items = cmdPickerEl.querySelectorAll('.chat-cmd-item');
    if (e.key === 'ArrowUp') {
      e.preventDefault();
      cmdSelectedIdx = (cmdSelectedIdx - 1 + items.length) % items.length;
      items.forEach((el, i) => el.classList.toggle('selected', i === cmdSelectedIdx));
      return;
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      cmdSelectedIdx = (cmdSelectedIdx + 1) % items.length;
      items.forEach((el, i) => el.classList.toggle('selected', i === cmdSelectedIdx));
      return;
    }
    if (e.key === 'Enter' || e.key === 'Tab') {
      e.preventDefault();
      const selected = items[cmdSelectedIdx];
      if (selected) {
        const cmdName = selected.querySelector('.chat-cmd-name').textContent;
        const cmd = CHAT_COMMANDS.find(c => c.name === cmdName);
        chatDismissCommands();
        if (cmd && cmd.dynamic) {
          chatInput.value = cmdName + ' ';
          chatInput.focus();
        } else {
          chatExecCommand(cmdName);
        }
      }
      return;
    }
    if (e.key === 'Escape') {
      chatDismissCommands();
      return;
    }
  }

  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    const text = chatInput.value.trim();
    // Check if it's a command
    if (text.startsWith('/')) {
      const cmd = CHAT_COMMANDS.find(c => c.name === text.split(' ')[0]);
      if (cmd && !cmd.dynamic) {
        chatDismissCommands();
        chatExecCommand(cmd.name);
        return;
      }
      // Dynamic commands (force tiers) are sent as regular messages -backend handles them.
    }
    chatSend();
  }
});
chatInput.addEventListener('input', () => {
  chatInput.style.height = '';
  chatInput.style.height = Math.min(chatInput.scrollHeight, 120) + 'px';

  const val = chatInput.value;
  if (val.startsWith('/') && !val.includes(' ')) {
    chatShowCommands(val);
  } else {
    chatDismissCommands();
  }
});

// --- Apps (sidebar nav) ---
function capitalizeName(name) {
  return name.replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
}

function loadApps() {
  // Fetch apps and marketplace state in parallel.
  return Promise.all([
    api('/api/apps/').catch(() => ({ items: [] })),
    api('/api/marketplace').catch(() => [])
  ]).then(([r, mpApps]) => {
    const section = document.getElementById('navAppsSection');
    const items = r.items || [];

    // Build marketplace state map: slug → state
    var mpState = {};
    (mpApps || []).forEach(mp => { mpState[mp.slug] = mp.state; });

    // Filter: marketplace apps (have manifest) only shown if enabled.
    // Local apps (no manifest/not in marketplace) always shown.
    var visibleItems = items.filter(app => {
      var state = mpState[app.name];
      if (state === undefined) return true; // local app, no marketplace entry
      return state === 'enabled';
    });

    section.innerHTML = '';

    const favs = JSON.parse(localStorage.getItem('alf-nav-favs') || '[]');
    visibleItems.forEach(app => {
      const a = document.createElement('a');
      a.className = 'nav-item';
      a.dataset.view = 'page:' + app.name;
      const icon = safeLucideIcon(app.icon || 'app-window', 'app-window');
      const label = app.display_name || capitalizeName(app.name);
      a.innerHTML = '<i data-lucide="' + esc(icon) + '"></i><span>' + esc(label) + '</span>';
      if (favs.includes(a.dataset.view)) a.classList.add('nav-fav');
      const pin = document.createElement('button');
      pin.className = 'nav-fav-btn';
      pin.title = 'Pin to favorites';
      pin.innerHTML = '<i data-lucide="pin"></i>';
      pin.addEventListener('click', (e) => {
        e.stopPropagation();
        e.preventDefault();
        a.classList.toggle('nav-fav');
        const current = JSON.parse(localStorage.getItem('alf-nav-favs') || '[]');
        const v = a.dataset.view;
        const updated = a.classList.contains('nav-fav')
          ? [...current.filter(f => f !== v), v]
          : current.filter(f => f !== v);
        localStorage.setItem('alf-nav-favs', JSON.stringify(updated));
      });
      a.appendChild(pin);
      a.addEventListener('click', () => navigateTo(a.dataset.view));
      section.appendChild(a);
    });

    // Restore active state
    const currentView = localStorage.getItem('alf-view');
    if (currentView && currentView.startsWith('page:')) {
      section.querySelectorAll('.nav-item').forEach(el => {
        el.classList.toggle('active', el.dataset.view === currentView);
      });
    }

    if (window.lucide) lucide.createIcons();
  }).catch(() => {});
}

// --- Docs ---
let docsCache = null;
let docsActiveFilter = { text: '', category: '', tag: '' };

function docsShowList() {
  const content = document.getElementById('docsContent');
  content.innerHTML = '<div style="color:var(--text-dim);font-size:0.85rem">Loading...</div>';
  api('/api/docs/').then(docs => {
    docsCache = docs;
    docsActiveFilter = { text: '', category: '', tag: '' };
    if (!docs || !docs.length) {
      content.innerHTML = '<div style="color:var(--text-dim);font-size:0.85rem">No documentation available.</div>';
      return;
    }
    docsRenderListLayout(docs);
  }).catch(() => {
    content.innerHTML = '<div style="color:var(--text-dim);font-size:0.85rem">Failed to load docs.</div>';
  });
}

function docsRenderListLayout(docs) {
  const content = document.getElementById('docsContent');

  // Extract categories and tags
  const categories = new Set();
  const tags = new Set();
  docs.forEach(d => {
    if (d.category) categories.add(d.category);
    (d.tags || []).forEach(t => tags.add(t));
  });

  let html = '<div class="docs-browse-layout">';

  // Sidebar with categories and tags
  if (categories.size > 0 || tags.size > 0) {
    html += '<aside class="docs-sidebar">';
    html += '<button class="docs-filter-item active" data-filter-type="all">All docs</button>';
    if (categories.size > 0) {
      html += '<div class="docs-filter-group">Categories</div>';
      categories.forEach(c => {
        const count = docs.filter(d => d.category === c).length;
        html += '<button class="docs-filter-item" data-filter-type="category" data-filter-value="' + esc(c) + '">' + esc(c) + ' <span class="docs-filter-count">' + count + '</span></button>';
      });
    }
    if (tags.size > 0) {
      html += '<div class="docs-filter-group">Tags</div>';
      html += '<div class="docs-tag-cloud">';
      tags.forEach(t => {
        html += '<button class="docs-tag-btn" data-filter-type="tag" data-filter-value="' + esc(t) + '">' + esc(t) + '</button>';
      });
      html += '</div>';
    }
    html += '</aside>';
  }

  // Main area: search + list
  html += '<div class="docs-main">';
  html += '<div class="docs-search"><input type="text" id="docsSearchInput" placeholder="Search docs..."><i data-lucide="search"></i></div>';
  html += '<div id="docsListContainer" class="docs-list"></div>';
  html += '</div></div>';

  content.innerHTML = html;

  // Bind search
  let docsSearchTimeout;
  document.getElementById('docsSearchInput').addEventListener('input', (e) => {
    docsActiveFilter.text = e.target.value.trim().toLowerCase();
    clearTimeout(docsSearchTimeout);
    if (docsActiveFilter.text) {
      // Debounce server-side search for content matching.
      docsSearchTimeout = setTimeout(() => {
        fetch('/api/docs/?q=' + encodeURIComponent(docsActiveFilter.text))
          .then(r => r.json())
          .then(data => { docsCache = data; docsUpdateList(); });
      }, 300);
    } else {
      // Empty query: reload full list.
      fetch('/api/docs/').then(r => r.json()).then(data => { docsCache = data; docsUpdateList(); });
    }
  });

  // Bind filter buttons
  content.querySelectorAll('[data-filter-type]').forEach(btn => {
    btn.addEventListener('click', () => {
      const type = btn.dataset.filterType;
      const value = btn.dataset.filterValue || '';
      if (type === 'all') {
        docsActiveFilter.category = '';
        docsActiveFilter.tag = '';
      } else if (type === 'category') {
        docsActiveFilter.category = docsActiveFilter.category === value ? '' : value;
        docsActiveFilter.tag = '';
      } else if (type === 'tag') {
        docsActiveFilter.tag = docsActiveFilter.tag === value ? '' : value;
        docsActiveFilter.category = '';
      }
      // Update active states
      content.querySelectorAll('.docs-filter-item').forEach(el => {
        el.classList.toggle('active',
          (type === 'all' && !docsActiveFilter.category && !docsActiveFilter.tag && el.dataset.filterType === 'all') ||
          (el.dataset.filterType === 'category' && el.dataset.filterValue === docsActiveFilter.category) ||
          (!docsActiveFilter.category && !docsActiveFilter.tag && el.dataset.filterType === 'all')
        );
      });
      content.querySelectorAll('.docs-tag-btn').forEach(el => {
        el.classList.toggle('active', el.dataset.filterValue === docsActiveFilter.tag);
      });
      docsUpdateList();
    });
  });

  if (window.lucide) lucide.createIcons();
  docsUpdateList();
}

function docsUpdateList() {
  const container = document.getElementById('docsListContainer');
  if (!container || !docsCache) return;

  let filtered = docsCache;
  if (docsActiveFilter.category) {
    filtered = filtered.filter(d => d.category === docsActiveFilter.category);
  }
  if (docsActiveFilter.tag) {
    filtered = filtered.filter(d => (d.tags || []).includes(docsActiveFilter.tag));
  }
  // Text search is handled server-side via /api/docs/?q= (searches content too).

  let html = '';
  if (!filtered.length) {
    html = '<div style="color:var(--text-dim);font-size:0.85rem;padding:12px 0">No results.</div>';
  }
  filtered.forEach(d => {
    html += '<div class="docs-list-item" data-doc-id="' + esc(d.id) + '">';
    if (d.category) html += '<span class="docs-item-category">' + esc(d.category) + '</span>';
    html += '<h3>' + esc(d.title) + '</h3>';
    if (d.summary) html += '<p>' + esc(d.summary) + '</p>';
    if (d.tags && d.tags.length) {
      html += '<div class="docs-item-tags">';
      d.tags.forEach(t => { html += '<span class="docs-item-tag">' + esc(t) + '</span>'; });
      html += '</div>';
    }
    html += '</div>';
  });
  container.innerHTML = html;
  container.querySelectorAll('.docs-list-item').forEach(el => {
    el.addEventListener('click', () => navigateTo('docs:' + el.dataset.docId));
  });
}

function docsShowArticle(id) {
  const content = document.getElementById('docsContent');
  content.innerHTML = '<div style="color:var(--text-dim);font-size:0.85rem">Loading...</div>';
  api('/api/docs/' + encodeURIComponent(id)).then(doc => {
    // Convert docs: links to data attributes before sanitizing (DOMPurify strips unknown protocols).
    let rawHtml = marked.parse(doc.content, { breaks: false, gfm: true });
    rawHtml = rawHtml.replace(/href="docs:([^"]+)"/g, 'href="#" data-doc-link="$1"');
    const rendered = DOMPurify.sanitize(rawHtml);

    // Build TOC from headings
    const tmp = document.createElement('div');
    tmp.innerHTML = rendered;
    const headings = tmp.querySelectorAll('h2, h3');
    let tocHtml = '';
    if (headings.length > 1) {
      tocHtml = '<nav class="docs-toc"><div class="docs-toc-title">Contents</div>';
      headings.forEach((h, i) => {
        const anchorId = 'docs-heading-' + i;
        h.id = anchorId;
        const level = h.tagName === 'H3' ? ' docs-toc-sub' : '';
        tocHtml += '<a class="docs-toc-item' + level + '" href="#' + anchorId + '">' + h.textContent + '</a>';
      });
      tocHtml += '</nav>';
    }

    let html = '<a class="docs-back" id="docsBackBtn"><i data-lucide="arrow-left"></i> Back to docs</a>';
    html += '<div class="docs-article-layout">';
    html += tocHtml;
    html += '<div class="docs-article">' + tmp.innerHTML + '</div>';
    html += '</div>';
    html += '<button class="docs-scroll-top" id="docsScrollTop" title="Back to top"><i data-lucide="chevron-up"></i></button>';
    content.innerHTML = html;

    document.getElementById('docsBackBtn').addEventListener('click', () => navigateTo('docs'));
    // Handle internal doc links (data-doc-link attribute, converted from docs: protocol)
    content.querySelectorAll('.docs-article a[data-doc-link]').forEach(a => {
      a.addEventListener('click', (e) => {
        e.preventDefault();
        navigateTo('docs:' + a.dataset.docLink);
      });
    });
    // Smooth scroll for TOC links
    content.querySelectorAll('.docs-toc-item').forEach(a => {
      a.addEventListener('click', (e) => {
        e.preventDefault();
        const target = document.querySelector(a.getAttribute('href'));
        if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
      });
    });
    // Scroll-to-top button visibility
    const scrollBtn = document.getElementById('docsScrollTop');
    const mainContent = document.querySelector('.main-content');
    if (scrollBtn && mainContent) {
      scrollBtn.addEventListener('click', () => mainContent.scrollTo({ top: 0, behavior: 'smooth' }));
      mainContent.addEventListener('scroll', () => {
        scrollBtn.classList.toggle('visible', mainContent.scrollTop > 400);
      });
    }
    if (window.lucide) lucide.createIcons();
  }).catch(() => {
    content.innerHTML = '<div style="color:var(--text-dim);font-size:0.85rem">Document not found.</div>';
  });
}

// ─── Schedules ──────────────────────────────────────
let schedulesCache = null;
let schedulesVisible = [];
let schedulesInitialized = false;
let schedulesFilter = 'all';
let schedulesSSE = null;
let schedCollapsedSet = new Set(); // track collapsed job IDs
let schedAllCollapsed = true; // default: all collapsed
let schedExpandedSet = new Set(); // track individually expanded job IDs

const OUTPUTS = ['chat', 'file', 'both', 'silent'];

function schedulesInit() {
  if (!schedulesInitialized) {
    schedulesInitialized = true;
    document.getElementById('schedulesAddBtn').addEventListener('click', () => schedulesShowModal(null));
    document.getElementById('schedCollapseAllBtn').addEventListener('click', () => {
      schedAllCollapsed = !schedAllCollapsed;
      schedExpandedSet.clear();
      schedCollapsedSet.clear();
      schedulesRender();
    });
    document.getElementById('schedFilters').addEventListener('click', e => {
      const btn = e.target.closest('.sched-filter');
      if (!btn) return;
      document.querySelectorAll('.sched-filter').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      schedulesFilter = btn.dataset.filter;
      schedulesRender();
    });
    schedulesConnectSSE();
    document.getElementById('schedSearch').addEventListener('input', () => schedulesRender());
  }
  schedulesLoad();
}

function schedulesConnectSSE() {
  if (schedulesSSE) { schedulesSSE.close(); schedulesSSE = null; }
  const es = new EventSource('/api/schedules/events');
  es.addEventListener('change', () => schedulesLoad());
  es.onerror = () => {
    es.close();
    schedulesSSE = null;
    setTimeout(schedulesConnectSSE, 5000);
  };
  schedulesSSE = es;
}

function schedulesLoad() {
  api('/api/schedules').then(data => {
    schedulesCache = data.jobs || [];
    schedulesRender();
  }).catch(() => toast('Failed to load schedules', 'error'));
}

function schedulesRender() {
  const list = document.getElementById('schedulesList');

  // Filter out internal system jobs -only show user/Alf-created ones.
  let filtered = (schedulesCache || []).filter(j => !j.system);

  // Sort by next_run descending (soonest first, no next_run at bottom).
  filtered.sort((a, b) => {
    if (!a.next_run && !b.next_run) return 0;
    if (!a.next_run) return 1;
    if (!b.next_run) return -1;
    return new Date(a.next_run) - new Date(b.next_run);
  });

  // Apply filter.
  const now = new Date();
  const todayEnd = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 23, 59, 59, 999);
  const weekEnd = new Date(todayEnd);
  weekEnd.setDate(weekEnd.getDate() + (7 - weekEnd.getDay()));

  if (schedulesFilter === 'recurring') {
    filtered = filtered.filter(j => !j.auto_delete);
  } else if (schedulesFilter === 'today') {
    filtered = filtered.filter(j => j.next_run && new Date(j.next_run) <= todayEnd);
  } else if (schedulesFilter === 'week') {
    filtered = filtered.filter(j => j.next_run && new Date(j.next_run) <= weekEnd);
  } else if (schedulesFilter === 'later') {
    filtered = filtered.filter(j => !j.next_run || new Date(j.next_run) > weekEnd);
  } else if (schedulesFilter === 'oneshot') {
    filtered = filtered.filter(j => j.auto_delete);
  } else if (schedulesFilter === 'managed') {
    filtered = filtered.filter(j => j.managed);
  } else if (schedulesFilter === 'obsolete') {
    filtered = filtered.filter(j => j.auto_delete && (!j.next_run || new Date(j.next_run) < now));
  }

  // Apply search filter.
  const schedQ = (document.getElementById('schedSearch')?.value || '').toLowerCase();
  if (schedQ) {
    filtered = filtered.filter(j => {
      return (j.name || '').toLowerCase().includes(schedQ) ||
        (j.prompt || '').toLowerCase().includes(schedQ) ||
        (j.message || '').toLowerCase().includes(schedQ) ||
        (j.command || '').toLowerCase().includes(schedQ) ||
        (j.id || '').toLowerCase().includes(schedQ);
    });
  }

  schedulesVisible = filtered;
  const visible = schedulesVisible;

  if (!visible.length) {
    const msg = schedulesFilter === 'all'
      ? 'No scheduled jobs yet.<br><span style="font-size:0.8rem;opacity:0.7">Create jobs to run prompts or commands on a schedule.</span>'
      : 'No jobs match this filter.';
    list.innerHTML = '<div class="task-empty"><div class="task-empty-icon">&#128197;</div>' + msg + '</div>';
    return;
  }

  list.innerHTML = visible.map((j, i) => {
    const statusDot = j.enabled ? '<span class="dot green"></span>' : '<span class="dot red"></span>';
    const badges = [];
    if (j.system) badges.push('<span class="tier-badge tier-badge-routable">system</span>');
    if (j.managed) badges.push('<span class="tier-badge tier-badge-instant">managed</span>');
    if (j.auto_delete) badges.push('<span class="tier-badge tier-badge-force">one-shot</span>');
    const isReminder = !!j.message;
    const tierLabel = isReminder ? 'reminder' : (j.tier === 'direct' ? 'direct (bash)' : (j.tier || 'default'));
    const outputBadge = '<span class="tier-badge tier-badge-routable">' + esc(j.output || 'telegram') + '</span>';
    const nextRun = j.next_run ? schedRelTime(j.next_run) : '--';
    const lastRun = j.last_run ? schedRelTime(j.last_run) : '--';

    // Content row: message for reminders, command for direct, prompt for LLM.
    let contentLabel, contentValue;
    if (isReminder) {
      contentLabel = 'Message';
      contentValue = j.message || '--';
    } else if (j.tier === 'direct') {
      contentLabel = 'Command';
      contentValue = j.command || '--';
    } else {
      contentLabel = 'Prompt';
      contentValue = j.prompt || '--';
      // Hide internal sentinel prompts for managed jobs.
      if (j.managed && contentValue.startsWith('__') && contentValue.endsWith('__')) {
        contentLabel = 'Type';
        contentValue = contentValue.replace(/__/g, '') + ' (reads context/' + contentValue.replace(/__/g, '') + '.md at runtime)';
      }
    }

    const canFullEdit = !j.managed && !j.system;
    const runBtn = j.enabled ? '<button class="btn-sm sched-run-btn" data-idx="' + i + '" title="Run now">Run</button>' : '';
    const toggleBtn = '<button class="btn-sm sched-toggle-btn" data-idx="' + i + '">' + (j.enabled ? 'Disable' : 'Enable') + '</button>';
    const actions = canFullEdit
      ? runBtn + toggleBtn +
        '<button class="btn-sm sched-edit-btn" data-idx="' + i + '">Edit</button>' +
        '<button class="btn-sm btn-danger sched-delete-btn" data-idx="' + i + '">Delete</button>'
      : runBtn + toggleBtn +
        (j.managed ? '<button class="btn-sm sched-managed-edit-btn" data-idx="' + i + '">Settings</button>' : '');

    const isCollapsed = schedAllCollapsed ? !schedExpandedSet.has(j.id) : schedCollapsedSet.has(j.id);
    return '<div class="tier-card' + (isCollapsed ? ' collapsed' : '') + '" data-idx="' + i + '" data-sched-id="' + esc(j.id) + '">' +
      '<div class="tier-card-header">' +
        '<i data-lucide="chevron-down" class="sched-collapse-chevron" style="width:14px;height:14px"></i>' +
        '<div class="tier-card-title">' + statusDot + '<strong>' + esc(j.name) + '</strong></div>' +
        '<span class="tier-model-badge" style="color:var(--text-dim)">' + esc(j.schedule) + '</span>' +
        '<div class="tier-card-actions">' + actions + '</div>' +
      '</div>' +
      '<div class="tier-card-details">' +
        '<div class="tier-detail-row"><span class="tier-detail-label">ID</span><span class="tier-detail-value" style="font-family:monospace;font-size:0.75rem;opacity:0.7">' + esc(j.id) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Tier</span><span class="tier-detail-value">' + esc(tierLabel) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">' + esc(contentLabel) + '</span><span class="tier-detail-value sched-prompt">' + esc(contentValue.substring(0, 200)) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Output</span><span class="tier-detail-value">' + outputBadge + '</span></div>' +
        (j.timeout ? '<div class="tier-detail-row"><span class="tier-detail-label">Timeout</span><span class="tier-detail-value">' + esc(j.timeout) + '</span></div>' : '') +
        '<div class="tier-detail-row"><span class="tier-detail-label">Next run</span><span class="tier-detail-value">' + esc(nextRun) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Last run</span><span class="tier-detail-value">' + esc(lastRun) + '</span></div>' +
        (j.last_error ? '<div class="tier-detail-row"><span class="tier-detail-label">Last error</span><span class="tier-detail-value" style="color:var(--red)">' + esc(j.last_error.substring(0, 200)) + '</span></div>' : '') +
        (badges.length ? '<div class="tier-detail-row"><span class="tier-detail-label">Flags</span><span class="tier-detail-value">' + badges.join(' ') + '</span></div>' : '') +
        (j.skills && j.skills.length ? '<div class="tier-detail-row"><span class="tier-detail-label">Skills</span><span class="tier-detail-value">' + esc(j.skills.join(', ')) + '</span></div>' : '') +
      '</div>' +
    '</div>';
  }).join('');

  list.querySelectorAll('.sched-edit-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      schedulesShowModal(schedulesVisible[+btn.dataset.idx]);
    });
  });
  list.querySelectorAll('.sched-delete-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const j = schedulesVisible[+btn.dataset.idx];
      if (!confirm('Delete job "' + j.name + '"?')) return;
      api('/api/schedules?id=' + encodeURIComponent(j.id), { method: 'DELETE' })
        .then(() => { toast('Job deleted'); schedulesLoad(); })
        .catch(err => toast('Delete failed: ' + err.message, 'error'));
    });
  });
  list.querySelectorAll('.sched-run-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const j = schedulesVisible[+btn.dataset.idx];
      if (!confirm('Run "' + j.name + '" now?\n\nThis will execute the job immediately as a one-shot.')) return;
      btn.disabled = true;
      btn.textContent = 'Running...';
      api('/api/schedules/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: j.id }),
      }).then(() => { toast('Job triggered: ' + j.name); })
        .catch(err => toast('Run failed: ' + err.message, 'error'))
        .finally(() => { btn.disabled = false; btn.textContent = 'Run'; });
    });
  });
  list.querySelectorAll('.sched-toggle-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const j = schedulesVisible[+btn.dataset.idx];
      api('/api/schedules', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: j.id, fields: { enabled: j.enabled ? 'false' : 'true' } }),
      }).then(() => { toast(j.enabled ? 'Job disabled' : 'Job enabled'); schedulesLoad(); })
        .catch(err => toast('Toggle failed: ' + (err.error || err.message), 'error'));
    });
  });
  // Managed job settings (output + tier only).
  list.querySelectorAll('.sched-managed-edit-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const j = schedulesVisible[+btn.dataset.idx];
      const outputOpts = OUTPUTS.map(o => '<option value="' + o + '"' + (j.output === o ? ' selected' : '') + '>' + o + '</option>').join('');
      const old = document.getElementById('schedModal');
      if (old) old.remove();
      const html = '<div class="modal-backdrop" id="schedModal">' +
        '<div class="modal tier-modal">' +
          '<h3>Settings: ' + esc(j.name) + '</h3>' +
          '<div class="tier-form">' +
            '<div class="form-row"><label>Schedule</label><input type="text" id="sjMSchedule" value="' + esc(j.schedule || '') + '" placeholder="0 0 */2 * * *"></div>' +
            '<div class="form-row"><label>Tier</label><input type="text" id="sjMTier" value="' + esc(j.tier || '') + '" placeholder="haiku, sonnet..."></div>' +
            '<div class="form-row"><label>Output</label><select id="sjMOutput">' + outputOpts + '</select></div>' +
          '</div>' +
          '<div class="modal-actions">' +
            '<button class="btn" id="sjMSave">Save</button>' +
            '<button class="btn btn-secondary" id="sjMCancel">Cancel</button>' +
          '</div>' +
        '</div></div>';
      document.body.insertAdjacentHTML('beforeend', html);
      document.getElementById('sjMCancel').onclick = () => document.getElementById('schedModal').remove();
      document.getElementById('schedModal').addEventListener('click', ev => { if (ev.target.id === 'schedModal') document.getElementById('schedModal').remove(); });
      document.getElementById('sjMSave').onclick = () => {
        const fields = {};
        const schedule = document.getElementById('sjMSchedule').value.trim();
        const tier = document.getElementById('sjMTier').value.trim();
        const output = document.getElementById('sjMOutput').value;
        if (schedule !== (j.schedule || '')) fields.schedule = schedule;
        if (tier !== (j.tier || '')) fields.tier = tier;
        if (output !== (j.output || 'telegram')) fields.output = output;
        if (!Object.keys(fields).length) { document.getElementById('schedModal').remove(); return; }
        api('/api/schedules', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: j.id, fields }),
        }).then(() => { toast('Settings updated'); document.getElementById('schedModal').remove(); schedulesLoad(); })
          .catch(err => toast('Update failed: ' + (err.error || err.message), 'error'));
      };
    });
  });
  // Collapse/expand individual cards — click anywhere on the header row.
  list.querySelectorAll('.tier-card-header').forEach(header => {
    header.style.cursor = 'pointer';
    header.addEventListener('click', e => {
      // Don't toggle if clicking a button inside the header.
      if (e.target.closest('button')) return;
      const card = header.closest('.tier-card');
      const id = card.dataset.schedId;
      const isCurrentlyCollapsed = card.classList.contains('collapsed');
      if (isCurrentlyCollapsed) {
        schedExpandedSet.add(id);
        schedCollapsedSet.delete(id);
      } else {
        schedCollapsedSet.add(id);
        schedExpandedSet.delete(id);
      }
      card.classList.toggle('collapsed');
    });
  });
  // Update collapse-all button icon.
  const colBtn = document.getElementById('schedCollapseAllBtn');
  if (colBtn) {
    const icon = colBtn.querySelector('i[data-lucide]');
    if (icon) icon.setAttribute('data-lucide', schedAllCollapsed ? 'maximize-2' : 'minimize-2');
  }
  if (window.lucide) lucide.createIcons();
}

function schedulesShowModal(job) {
  const isEdit = !!job;
  const j = job || { name: '', schedule: '', tier: '', prompt: '', command: '', output: 'telegram', skills: [] };

  const old = document.getElementById('schedModal');
  if (old) old.remove();

  const isDirect = j.tier === 'direct';
  const outputOpts = OUTPUTS.map(o => '<option value="' + o + '"' + (j.output === o ? ' selected' : '') + '>' + o + '</option>').join('');

  const html = '<div class="modal-backdrop" id="schedModal">' +
    '<div class="modal tier-modal">' +
      '<h3>' + (isEdit ? 'Edit Job' : 'Add Job') + '</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Name</label><input type="text" id="sjName" value="' + esc(j.name) + '"' + (isEdit ? ' readonly style="opacity:0.6"' : '') + '></div>' +
        '<div class="form-row"><label>Schedule <span title="6-field cron: sec min hour day month weekday\n\n*  = every value\n*/N = every N units\n1-5 = range\n1,3,5 = list\n\nExamples:\n0 30 9 * * *    daily at 9:30am\n0 0 */2 * * *   every 2 hours\n0 0 9 * * 1-5   weekdays at 9am\n0 */15 * * * *  every 15 minutes\n30 0 0 * * *    daily at midnight +30s" style="opacity:0.5;cursor:help">&#9432;</span></label><input type="text" id="sjSchedule" value="' + esc(j.schedule) + '" placeholder="0 30 9 * * * (cron with seconds)"></div>' +
        '<div class="form-row"><label>Tier</label><input type="text" id="sjTier" value="' + esc(j.tier || '') + '" placeholder="haiku, sonnet, direct..."></div>' +
        '<div class="form-row" id="sjPromptRow"><label>Prompt</label><textarea id="sjPrompt" rows="3" placeholder="What should the LLM do?">' + esc(j.prompt || '') + '</textarea></div>' +
        '<div class="form-row" id="sjCommandRow" style="display:none"><label>Command</label><textarea id="sjCommand" rows="3" placeholder="Bash command to execute">' + esc(j.command || '') + '</textarea></div>' +
        '<div class="form-row"><label>Output</label><select id="sjOutput">' + outputOpts + '</select></div>' +
        '<div class="form-row"><label>Skills</label><input type="text" id="sjSkills" value="' + esc((j.skills || []).join(', ')) + '" placeholder="skill1, skill2 (optional)"></div>' +
      '</div>' +
      '<div class="upload-actions">' +
        '<button class="btn" id="schedModalCancel">Cancel</button>' +
        '<button class="btn btn-primary" id="schedModalSave">' + (isEdit ? 'Save' : 'Add') + '</button>' +
      '</div>' +
    '</div>' +
  '</div>';

  document.body.insertAdjacentHTML('beforeend', html);

  const tierInput = document.getElementById('sjTier');
  const promptRow = document.getElementById('sjPromptRow');
  const commandRow = document.getElementById('sjCommandRow');

  function togglePromptCommand() {
    const isDirect = tierInput.value.trim() === 'direct';
    promptRow.style.display = isDirect ? 'none' : '';
    commandRow.style.display = isDirect ? '' : 'none';
  }
  togglePromptCommand();
  if (isDirect) { commandRow.style.display = ''; promptRow.style.display = 'none'; }
  tierInput.addEventListener('input', togglePromptCommand);

  document.getElementById('schedModalCancel').addEventListener('click', () => document.getElementById('schedModal').remove());
  document.getElementById('schedModal').addEventListener('click', e => { if (e.target.id === 'schedModal') document.getElementById('schedModal').remove(); });

  document.getElementById('schedModalSave').addEventListener('click', () => {
    const name = document.getElementById('sjName').value.trim();
    const schedule = document.getElementById('sjSchedule').value.trim();
    const tier = document.getElementById('sjTier').value.trim();
    const prompt = document.getElementById('sjPrompt').value.trim();
    const command = document.getElementById('sjCommand').value.trim();
    const output = document.getElementById('sjOutput').value;
    const skillsRaw = document.getElementById('sjSkills').value.trim();
    const skills = skillsRaw ? skillsRaw.split(',').map(s => s.trim()).filter(Boolean) : [];

    if (!name) { toast('Name is required', 'error'); return; }
    if (!schedule) { toast('Schedule is required', 'error'); return; }

    if (isEdit) {
      const fields = {};
      if (schedule !== j.schedule) fields.schedule = schedule;
      if (tier !== j.tier) fields.tier = tier;
      if (prompt !== (j.prompt || '')) fields.prompt = prompt;
      if (command !== (j.command || '')) fields.command = command;
      if (output !== j.output) fields.output = output;
      if (Object.keys(fields).length === 0) {
        document.getElementById('schedModal').remove();
        return;
      }
      api('/api/schedules', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: j.id, fields }),
      }).then(() => {
        toast('Job updated');
        document.getElementById('schedModal').remove();
        schedulesLoad();
      }).catch(err => toast('Update failed: ' + err.message, 'error'));
    } else {
      const body = { name, schedule, output };
      if (tier) body.tier = tier;
      if (tier === 'direct') { body.command = command; } else { body.prompt = prompt; }
      if (skills.length) body.skills = skills;
      api('/api/schedules', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      }).then(() => {
        toast('Job created');
        document.getElementById('schedModal').remove();
        schedulesLoad();
      }).catch(err => toast('Create failed: ' + err.message, 'error'));
    }
  });
}

function schedRelTime(iso) {
  if (!iso) return '--';
  const d = new Date(iso);
  const now = new Date();
  const diff = d - now;
  const abs = Math.abs(diff);
  const secs = Math.floor(abs / 1000);
  if (secs < 60) return (diff > 0 ? 'in ' : '') + secs + 's' + (diff <= 0 ? ' ago' : '');
  const mins = Math.floor(secs / 60);
  if (mins < 60) return (diff > 0 ? 'in ' : '') + mins + 'm' + (diff <= 0 ? ' ago' : '');
  const hours = Math.floor(mins / 60);
  if (hours < 24) return (diff > 0 ? 'in ' : '') + hours + 'h' + (diff <= 0 ? ' ago' : '');
  const days = Math.floor(hours / 24);
  return (diff > 0 ? 'in ' : '') + days + 'd' + (diff <= 0 ? ' ago' : '');
}

// ─── Tasks ──────────────────────────────────────────
let tasksInitialized = false;
var tasksRunningIds = new Set(); // track running task IDs for completion detection
var tasksArbitrationNotified = new Set(); // track tasks we've already notified about

async function taskLauncherLoadTeams() {
  const sel = document.getElementById('taskLauncherTeam');
  try {
    const data = await api('/api/teams');
    const teams = data.teams || [];
    // Keep the first "Auto" option, clear the rest.
    while (sel.options.length > 1) sel.remove(1);
    for (const t of teams) {
      const opt = document.createElement('option');
      opt.value = t.name;
      opt.textContent = t.name + (t.description ? ' -' + t.description : '');
      sel.appendChild(opt);
    }
  } catch {}
}

function tasksStopAutoRefresh() {
  if (tasksAutoTimer) { clearInterval(tasksAutoTimer); tasksAutoTimer = null; }
}

function tasksStartAutoRefresh() {
  tasksStopAutoRefresh();
  tasksAutoTimer = setInterval(tasksFetch, 3000);
}

function tasksInit() {
  if (!tasksInitialized) {
    tasksInitialized = true;
    document.getElementById('tasksRefreshBtn').addEventListener('click', () => tasksFetch());
    document.getElementById('tasksAutoRefresh').addEventListener('change', (e) => {
      if (e.target.checked) tasksStartAutoRefresh(); else tasksStopAutoRefresh();
    });
    // Task launcher: send prompt to agent tier.
    const launchBtn = document.getElementById('taskLaunchBtn');
    const launchInput = document.getElementById('taskLauncherInput');
    const launchTeam = document.getElementById('taskLauncherTeam');
    taskLauncherLoadTeams();
    launchBtn.addEventListener('click', async () => {
      const prompt = launchInput.value.trim();
      if (!prompt) return;
      const team = launchTeam.value;
      const message = team ? '[Use team: ' + team + ']\n' + prompt : prompt;
      const needValidation = document.getElementById('taskNeedValidation').checked;
      launchBtn.disabled = true;
      launchBtn.innerHTML = '<span class="dot-pulse"><span></span><span></span><span></span></span> Launching...';
      try {
        const res = await fetch('/api/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'same-origin',
          body: JSON.stringify({ message: message, need_validation: needValidation }),
        });
        if (!res.ok) throw new Error('status ' + res.status);
        launchInput.value = '';
        toast('Task launched');
        setTimeout(() => tasksFetch(), 1500);
      } catch (e) {
        toast('Launch failed: ' + e.message, 'error');
      }
      launchBtn.disabled = false;
      launchBtn.innerHTML = '<i data-lucide="play" style="width:14px;height:14px;vertical-align:middle;margin-right:4px"></i>Launch';
      lucide.createIcons({ nodes: [launchBtn] });
    });
    launchInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        launchBtn.click();
      }
    });
  }
  tasksFetch();
  if (document.getElementById('tasksAutoRefresh').checked) tasksStartAutoRefresh();
}

async function tasksFetch() {
  try {
    const res = await fetch('/api/tasks');
    const data = await res.json();
    const running = data.running || [];
    const completed = data.completed || [];

    // Detect newly completed tasks (were running, now in completed).
    const newRunningIds = new Set(running.map(t => t.id));
    for (const t of completed) {
      if (tasksRunningIds.has(t.id)) {
        const status = t.status === 'completed' ? 'completed' : 'failed';
        const preview = (t.prompt || '').substring(0, 80);
        toast('Task ' + status + ': ' + preview.substring(0, 60));
        notify('Agent Task ' + status, preview);
      }
    }
    // Detect arbitration/approval status changes on running tasks.
    for (const t of running) {
      if (t.status === 'awaiting_arbitration' && !tasksArbitrationNotified.has(t.id)) {
        tasksArbitrationNotified.add(t.id);
        toast('Task needs your input');
        notify('Agent Task needs input', (t.prompt || '').substring(0, 80));
      } else if (t.status === 'awaiting_approval' && !tasksArbitrationNotified.has(t.id + ':approval')) {
        tasksArbitrationNotified.add(t.id + ':approval');
        toast('Task plan ready for review');
        notify('Agent Task plan ready', (t.prompt || '').substring(0, 80));
      }
    }
    tasksRunningIds = newRunningIds;

    tasksRender(running, completed);
  } catch (e) {
    document.getElementById('tasksList').innerHTML = '<div class="task-empty">Failed to load tasks</div>';
  }
}

// Track which task cards and agent steps are expanded so refresh doesn't collapse them.
let tasksExpandedSet = new Set();
let tasksStepExpandedSet = new Set();
// Track whether completed section is collapsed (default: collapsed).
let tasksCompletedVisible = false;

// Last JSON snapshot to detect actual changes.
let tasksLastJSON = '';

function tasksRender(running, completed) {
  const container = document.getElementById('tasksList');

  // Skip rebuild if a textarea inside the tasks pane has focus (user is typing).
  const tasksPane = document.getElementById('tasksRunsPane');
  const activeEl = document.activeElement;
  if (activeEl && activeEl.tagName === 'TEXTAREA' && tasksPane && tasksPane.contains(activeEl)) {
    return;
  }

  // Skip rebuild if nothing changed (avoids flicker and scroll jumps).
  const snapshot = JSON.stringify({ r: running, c: completed });
  if (snapshot === tasksLastJSON) return;
  tasksLastJSON = snapshot;

  // Save scroll state before rebuild.
  const savedScrollTop = tasksPane ? tasksPane.scrollTop : 0;

  if (running.length === 0 && completed.length === 0) {
    container.innerHTML = '<div class="task-empty"><i data-lucide="bot" style="width:40px;height:40px;opacity:0.3;margin-bottom:8px"></i><br>No agent tasks yet.<br><span style="font-size:0.8rem;opacity:0.7">Tasks appear here when you use the agent tier.</span></div>';
    return;
  }

  // Auto-expand tasks awaiting approval or arbitration.
  for (const task of running) {
    if (task.status === 'awaiting_approval' || task.status === 'awaiting_arbitration') tasksExpandedSet.add(task.id);
  }

  let html = '';

  if (running.length > 0) {
    html += '<h3 class="tasks-section-title"><i data-lucide="zap" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Running (' + running.length + ')</h3>';
    for (const task of running) {
      html += taskCard(task, true);
    }
  }

  if (completed.length > 0) {
    const chevronDir = tasksCompletedVisible ? 'chevron-down' : 'chevron-right';
    html += '<h3 class="tasks-section-title tasks-section-toggle" id="tasksCompletedToggle">' +
      '<i data-lucide="' + chevronDir + '" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>' +
      'Recent (' + completed.length + ')</h3>';
    html += '<div class="tasks-completed-list" id="tasksCompletedList" style="display:' + (tasksCompletedVisible ? 'flex' : 'none') + '">';
    for (const task of completed) {
      html += taskCard(task, false);
    }
    html += '</div>';
  }

  container.innerHTML = html;

  // Bind completed section toggle.
  const toggle = document.getElementById('tasksCompletedToggle');
  if (toggle) {
    toggle.onclick = () => {
      tasksCompletedVisible = !tasksCompletedVisible;
      const list = document.getElementById('tasksCompletedList');
      list.style.display = tasksCompletedVisible ? 'flex' : 'none';
      const icon = toggle.querySelector('.lucide, i[data-lucide]');
      if (icon) {
        icon.setAttribute('data-lucide', tasksCompletedVisible ? 'chevron-down' : 'chevron-right');
        lucide.createIcons({ attrs: { class: ['lucide'] }, nameAttr: 'data-lucide' });
      }
    };
  }

  container.querySelectorAll('.task-cancel-btn').forEach(btn => {
    btn.onclick = (e) => { e.stopPropagation(); tasksCancel(btn.dataset.id); };
  });

  container.querySelectorAll('.task-relaunch-btn').forEach(btn => {
    btn.onclick = (e) => {
      e.stopPropagation();
      const status = btn.dataset.status || '';
      const hint = (status === 'timeout' || status === 'failed') ? '\n\n[Note: previous attempt ' + status + '. ' : '';
      agentModalShow(btn.dataset.prompt, hint);
    };
  });

  container.querySelectorAll('.task-delete-btn').forEach(btn => {
    btn.onclick = (e) => { e.stopPropagation(); tasksDelete(btn.dataset.id); };
  });

  // Restore expanded state and bind click handlers.
  container.querySelectorAll('.task-card').forEach(card => {
    const id = card.dataset.taskId;
    if (id && tasksExpandedSet.has(id)) {
      card.classList.add('expanded');
    }
  });

  container.querySelectorAll('.task-card-header').forEach(header => {
    header.onclick = () => {
      const card = header.closest('.task-card');
      const id = card.dataset.taskId;
      card.classList.toggle('expanded');
      if (id) {
        if (card.classList.contains('expanded')) tasksExpandedSet.add(id);
        else tasksExpandedSet.delete(id);
      }
    };
  });

  // Restore expanded state for agent steps and bind toggles.
  container.querySelectorAll('.task-step').forEach(step => {
    const key = step.dataset.stepKey;
    if (key && tasksStepExpandedSet.has(key)) {
      step.classList.add('step-expanded');
    }
  });
  container.querySelectorAll('.task-step-header').forEach(header => {
    header.onclick = () => {
      const step = header.closest('.task-step');
      step.classList.toggle('step-expanded');
      const key = step.dataset.stepKey;
      if (key) {
        if (step.classList.contains('step-expanded')) tasksStepExpandedSet.add(key);
        else tasksStepExpandedSet.delete(key);
      }
    };
  });

  // Bind approval buttons.
  container.querySelectorAll('.task-approve-btn').forEach(btn => {
    btn.onclick = (e) => { e.stopPropagation(); tasksApprove(btn.dataset.id, true, ''); };
  });
  container.querySelectorAll('.task-reject-btn').forEach(btn => {
    btn.onclick = (e) => {
      e.stopPropagation();
      const textarea = btn.closest('.task-approval').querySelector('.task-approval-feedback');
      const feedback = textarea ? textarea.value.trim() : '';
      tasksApprove(btn.dataset.id, false, feedback);
    };
  });

  lucide.createIcons({ attrs: { class: ['lucide'] }, nameAttr: 'data-lucide' });

  // Restore scroll position after rebuild.
  if (tasksPane) tasksPane.scrollTop = savedScrollTop;
}

function taskRenderMd(text) {
  if (!text) return '';
  try {
    return DOMPurify.sanitize(marked.parse(text, { breaks: true, gfm: true }));
  } catch (e) {
    return chatRenderMd(text);
  }
}

function taskCard(task, isRunning) {
  const elapsed = isRunning
    ? taskElapsed(task.started_at, null)
    : taskElapsed(task.started_at, task.completed_at);
  const isAwaiting = task.status === 'awaiting_approval' || task.status === 'awaiting_arbitration';
  const statusClass = isAwaiting ? task.status : (isRunning ? 'running' : (task.status === 'completed' ? 'completed' : (task.status === 'timeout' ? 'timeout' : (task.status === 'interrupted' ? 'interrupted' : 'failed'))));
  const statusLabel = task.status === 'awaiting_approval' ? 'awaiting approval' : (task.status === 'awaiting_arbitration' ? 'needs input' : (isRunning ? 'running' : task.status));
  const cost = task.total_cost_usd ? '$' + task.total_cost_usd.toFixed(4) : '--';
  const promptPreview = taskEscapeHtml(task.prompt || 'No prompt').substring(0, 200);
  const agentCount = (task.agent_calls && task.agent_calls.length) || 0;
  const shortId = task.id ? task.id.substring(0, 8) : '--';

  // Full request section (collapsible).
  const fullPrompt = task.prompt ? '<div class="task-section"><details><summary class="task-section-title"><i data-lucide="message-square" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Request</summary><div class="task-section-body task-md">' + taskRenderMd(task.prompt) + '</div></details></div>' : '';

  // Plan section (collapsible).
  let planSection = '';
  if (task.plan && task.plan.length > 0) {
    let planSteps = '';
    task.plan.forEach(function(step) {
      const agentList = step.agents && step.agents.length > 0
        ? ' <span class="step-agents">' + step.agents.map(a => taskEscapeHtml(a)).join(', ') + '</span>'
        : '';
      planSteps += '<div class="task-plan-step"><span class="step-num">' + step.step + '.</span><span>' + taskRenderMd(step.description) + agentList + '</span></div>';
    });
    planSection = '<div class="task-section"><details><summary class="task-section-title"><i data-lucide="list-ordered" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Plan (' + task.plan.length + ' steps)</summary><div class="task-plan">' + planSteps + '</div></details></div>';
  }

  // Approval / arbitration section.
  let approvalSection = '';
  if (isAwaiting) {
    let questionsHtml = '';
    if (task.status === 'awaiting_arbitration' && task.questions && task.questions.length > 0) {
      questionsHtml = '<div class="task-questions"><div class="task-questions-title">Questions from agents</div><ol>' +
        task.questions.map(q => '<li>' + taskEscapeHtml(q) + '</li>').join('') + '</ol></div>';
    }
    const placeholder = task.status === 'awaiting_arbitration' ? 'Your answers...' : 'Optional feedback for revision...';
    const approveLabel = task.status === 'awaiting_arbitration' ? 'Submit' : 'Approve';
    approvalSection = '<div class="task-approval">' +
      questionsHtml +
      '<textarea class="task-approval-feedback" id="taskFeedback_' + taskEscapeHtml(task.id) + '" placeholder="' + placeholder + '" rows="2"></textarea>' +
      '<div class="task-approval-actions">' +
        '<button class="btn btn-sm btn-green task-approve-btn" data-id="' + taskEscapeHtml(task.id) + '"><i data-lucide="check" style="width:14px;height:14px;vertical-align:middle;margin-right:4px"></i>' + approveLabel + '</button>' +
        (task.status !== 'awaiting_arbitration' ? '<button class="btn btn-sm btn-danger task-reject-btn" data-id="' + taskEscapeHtml(task.id) + '"><i data-lucide="message-square-x" style="width:14px;height:14px;vertical-align:middle;margin-right:4px"></i>Request Changes</button>' : '') +
      '</div>' +
    '</div>';
  }

  // Output section (rendered as markdown via marked).
  const output = task.response ? '<div class="task-section"><div class="task-section-title"><i data-lucide="file-text" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Output</div><div class="task-section-body task-md">' + taskRenderMd(task.response) + '</div></div>' : '';

  // Agent calls section.
  let agentSteps = '';
  if (agentCount > 0) {
    const workingCount = task.agent_calls.filter(function(c) { return c.status === 'working'; }).length;
    const doneCount = agentCount - workingCount;
    const agentSummary = workingCount > 0
      ? agentCount + ' agents (' + workingCount + ' working, ' + doneCount + ' done)'
      : agentCount + ' agents';
    agentSteps = '<div class="task-steps"><div class="task-steps-title"><i data-lucide="users" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Agent calls \u2014 ' + agentSummary + '</div>';
    task.agent_calls.forEach(function(call, idx) {
      const stepKey = task.id + ':' + idx;
      const agentParts = call.agent.split('/');
      const teamName = agentParts.length > 1 ? agentParts[0] : '';
      const agentName = agentParts.pop();
      const isWorking = call.status === 'working';
      const callStatus = isWorking ? 'working' : (call.error ? 'failed' : 'completed');
      const callCost = call.cost_usd ? '$' + call.cost_usd.toFixed(4) : '';
      const callTask = call.task ? '<div class="task-step-task"><details><summary>Instructions</summary><div class="task-step-task-body task-md">' + taskRenderMd(call.task) + '</div></details></div>' : '';
      const callResult = isWorking ? ''
        : (call.error ? '<pre class="task-step-error">' + taskEscapeHtml(call.error) + '</pre>' : taskRenderMd(call.text || ''));
      // Meta badges: team, model.
      let metaBadges = '';
      if (teamName) metaBadges += '<span class="task-step-meta">' + taskEscapeHtml(teamName) + '</span>';
      if (call.model) metaBadges += '<span class="task-step-meta">' + taskEscapeHtml(call.model) + '</span>';

      agentSteps += '<div class="task-step ' + callStatus + '" data-step-key="' + taskEscapeHtml(stepKey) + '">' +
        '<div class="task-step-header">' +
          '<span class="task-step-icon"><i data-lucide="' + (isWorking ? 'loader' : (call.error ? 'x-circle' : 'check-circle')) + '"></i></span>' +
          '<span class="task-step-agent">' + taskEscapeHtml(agentName) + '</span>' +
          metaBadges +
          (isWorking ? '<span class="task-step-badge working"><i data-lucide="loader" style="width:10px;height:10px;vertical-align:middle;margin-right:2px"></i>working</span>' : '') +
          '<span class="task-step-cost">' + callCost + '</span>' +
          (callResult ? '<span class="task-step-toggle"><i data-lucide="chevron-right"></i></span>' : '') +
        '</div>' +
        callTask +
        (callResult ? '<div class="task-step-result task-md">' + callResult + '</div>' : '') +
        '</div>';
    });
    agentSteps += '</div>';
  }

  const statusDot = '<span class="dot ' + (isRunning ? 'blue' : statusClass === 'completed' ? 'green' : 'red') + '"></span>';
  const cancelBtn = isRunning
    ? '<button class="btn-sm btn-danger task-cancel-btn" data-id="' + taskEscapeHtml(task.id) + '"><i data-lucide="square" style="width:12px;height:12px;vertical-align:middle;margin-right:2px"></i>Cancel</button>'
    : '';
  const relaunchBtn = !isRunning && task.prompt
    ? '<button class="btn-sm task-relaunch-btn" data-prompt="' + taskEscapeHtml(task.prompt).replace(/"/g, '&quot;') + '" data-status="' + taskEscapeHtml(task.status) + '"><i data-lucide="rotate-cw" style="width:12px;height:12px;vertical-align:middle;margin-right:2px"></i>Relaunch</button>'
    : '';
  const deleteBtn = !isRunning
    ? '<button class="btn-sm btn-danger task-delete-btn" data-id="' + taskEscapeHtml(task.id) + '"><i data-lucide="trash-2" style="width:12px;height:12px;vertical-align:middle;margin-right:2px"></i></button>'
    : '';

  return '<div class="task-card ' + statusClass + '" data-task-id="' + taskEscapeHtml(task.id) + '">' +
    '<div class="task-card-header">' +
      '<div class="task-card-title">' + statusDot + '<span class="task-id">#' + shortId + '</span><strong>' + promptPreview + '</strong></div>' +
      '<span class="task-status-badge ' + statusClass + '">' + statusLabel + '</span>' +
      '<div class="task-card-actions">' +
        relaunchBtn +
        cancelBtn +
        deleteBtn +
        '<span class="task-chevron"><i data-lucide="chevron-right"></i></span>' +
      '</div>' +
    '</div>' +
    '<div class="task-card-details">' +
      '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="clock" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Elapsed</span><span class="task-detail-value">' + elapsed + '</span></div>' +
      '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="coins" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Cost</span><span class="task-detail-value">' + cost + '</span></div>' +
      '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="repeat" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Iterations</span><span class="task-detail-value">' + (task.iterations || 0) + '</span></div>' +
      (agentCount > 0 ? '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="users" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Agents</span><span class="task-detail-value">' + agentCount + '</span></div>' : '') +
      (task.source ? '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="zap" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Source</span><span class="task-detail-value">' + taskEscapeHtml(task.source) + (task.team ? ' / ' + taskEscapeHtml(task.team) : '') + '</span></div>' : '') +
    '</div>' +
    fullPrompt +
    planSection +
    approvalSection +
    output +
    agentSteps +
  '</div>';
}

function taskElapsed(startedAt, completedAt) {
  if (!startedAt) return '--';
  const start = new Date(startedAt);
  const end = completedAt ? new Date(completedAt) : new Date();
  const diffMs = end - start;
  const secs = Math.floor(diffMs / 1000);
  if (secs < 60) return secs + 's';
  const mins = Math.floor(secs / 60);
  if (mins < 60) return mins + 'm ' + (secs % 60) + 's';
  const hours = Math.floor(mins / 60);
  return hours + 'h ' + (mins % 60) + 'm';
}

async function tasksCancel(id) {
  try {
    await fetch('/api/tasks?id=' + encodeURIComponent(id), { method: 'DELETE' });
    tasksFetch();
  } catch (e) {
    toast('Cancel failed: ' + e.message, 'error');
  }
}

async function tasksDelete(id) {
  try {
    await fetch('/api/tasks?id=' + encodeURIComponent(id) + '&action=delete', { method: 'DELETE' });
    toast('Task deleted');
    tasksFetch();
  } catch (e) {
    toast('Delete failed: ' + e.message, 'error');
  }
}

async function tasksApprove(id, approved, feedback) {
  try {
    const res = await fetch('/api/tasks/approve', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ id, approved, feedback }),
    });
    if (!res.ok) throw new Error('status ' + res.status);
    toast(approved ? 'Plan approved' : 'Changes requested');
    setTimeout(() => tasksFetch(), 500);
  } catch (e) {
    toast('Approval failed: ' + e.message, 'error');
  }
}

function taskEscapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// --- Agent Send Modal ---
async function agentModalShow(prompt, hint) {
  const modal = document.getElementById('agentModal');
  const textarea = document.getElementById('agentModalPrompt');
  textarea.value = prompt + (hint || '');
  textarea.readOnly = false;
  document.getElementById('agentModalValidation').checked = false;
  // Load teams into dropdown.
  const sel = document.getElementById('agentModalTeam');
  while (sel.options.length > 1) sel.remove(1);
  try {
    const data = await api('/api/teams');
    for (const t of (data.teams || [])) {
      const opt = document.createElement('option');
      opt.value = t.name;
      opt.textContent = t.name + (t.description ? ' - ' + t.description : '');
      sel.appendChild(opt);
    }
  } catch {}
  modal.style.display = 'flex';
  lucide.createIcons({ nodes: [modal] });
}

(function initAgentModal() {
  const modal = document.getElementById('agentModal');
  const cancelBtn = document.getElementById('agentModalCancel');
  const sendBtn = document.getElementById('agentModalSend');

  cancelBtn.addEventListener('click', () => { modal.style.display = 'none'; });
  modal.addEventListener('click', (e) => { if (e.target === modal) modal.style.display = 'none'; });
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape' && modal.style.display !== 'none') modal.style.display = 'none'; });

  sendBtn.addEventListener('click', async () => {
    const prompt = document.getElementById('agentModalPrompt').value;
    const team = document.getElementById('agentModalTeam').value;
    const needValidation = document.getElementById('agentModalValidation').checked;
    const message = team ? '[Use team: ' + team + ']\n' + prompt : prompt;
    sendBtn.disabled = true;
    try {
      const res = await fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({ message: message, need_validation: needValidation }),
      });
      if (res.ok) {
        toast('Task launched');
        modal.style.display = 'none';
      } else {
        toast('Failed to launch task', 'error');
      }
    } catch { toast('Failed to launch task', 'error'); }
    sendBtn.disabled = false;
  });
})();

// --- Teams Management ---
const teamsTemplate = {
  name: "my-team",
  description: "What this team does",
  max_agents_per_request: 3,
  global_timeout_minutes: 15,
  agents: [{
    name: "agent-name",
    description: "What this agent does",
    model: "sonnet",
    system_prompt: "You are a specialist in...",
    tools: [],
    write_capable: false,
    max_turns: 10,
    effort: "medium"
  }]
};

let teamsInitialized = false;
function teamsInit() {
  if (!teamsInitialized) {
    teamsInitialized = true;
    teamsInitEditor();
  }
  teamsFetch();
}

async function teamsFetch() {
  try {
    const data = await api('/api/teams');
    teamsRender(data.teams || []);
  } catch (e) {
    document.getElementById('teamsList').innerHTML = '<div class="task-empty">Failed to load teams</div>';
  }
}

function teamsRender(teams) {
  const container = document.getElementById('teamsList');
  if (teams.length === 0) {
    container.innerHTML = '<div class="task-empty"><i data-lucide="users" style="width:40px;height:40px;opacity:0.3;margin-bottom:8px"></i><br>No agent teams configured.<br><span style="font-size:0.8rem;opacity:0.7">Click "New Team" to add one.</span></div>';
    lucide.createIcons({ attrs: { class: ['lucide'] }, nameAttr: 'data-lucide' });
    return;
  }
  let html = '';
  for (const team of teams) {
    const agentBadges = (team.agents || []).map(a =>
      '<span class="team-agent-badge">' + taskEscapeHtml(a.name) + ' (' + taskEscapeHtml(a.tier || '?') + ')</span>'
    ).join('');
    html += '<div class="team-card" data-team-name="' + taskEscapeHtml(team.name) + '">' +
      '<div class="team-card-header">' +
        '<span class="team-card-name">' + taskEscapeHtml(team.name) + '</span>' +
        '<div class="team-card-actions">' +
          '<button class="btn btn-sm btn-icon team-edit-btn" title="Edit"><i data-lucide="edit-2" style="width:14px;height:14px"></i></button>' +
          '<button class="btn btn-sm btn-icon btn-danger team-delete-btn" title="Delete"><i data-lucide="trash-2" style="width:14px;height:14px"></i></button>' +
        '</div>' +
      '</div>' +
      (team.description ? '<div class="team-card-desc">' + taskEscapeHtml(team.description) + '</div>' : '') +
      '<div class="team-card-agents">' + agentBadges + '</div>' +
    '</div>';
  }
  container.innerHTML = html;
  lucide.createIcons({ attrs: { class: ['lucide'] }, nameAttr: 'data-lucide' });

  // Bind edit buttons.
  container.querySelectorAll('.team-edit-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const card = btn.closest('.team-card');
      const name = card.dataset.teamName;
      const team = teams.find(t => t.name === name);
      if (team) teamsOpenEditor(team);
    });
  });
  // Bind delete buttons.
  container.querySelectorAll('.team-delete-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const card = btn.closest('.team-card');
      const name = card.dataset.teamName;
      if (confirm('Delete team "' + name + '"?')) teamsDelete(name);
    });
  });
}

function teamsInitEditor() {
  document.getElementById('teamsAddBtn').addEventListener('click', () => {
    teamsOpenEditor(null);
  });
  document.getElementById('teamsRefreshBtn').addEventListener('click', () => teamsFetch());
  document.getElementById('teamsEditorSave').addEventListener('click', () => teamsSave());
  document.getElementById('teamsEditorCancel').addEventListener('click', () => teamsCloseEditor());
}

function teamsOpenEditor(team) {
  const editor = document.getElementById('teamsEditor');
  const textarea = document.getElementById('teamsEditorJson');
  const title = document.getElementById('teamsEditorTitle');
  const hint = document.getElementById('teamsEditorHint');

  editor.style.display = '';
  hint.textContent = '';
  hint.className = 'teams-editor-hint';

  if (team) {
    title.textContent = 'Edit: ' + team.name;
    textarea.value = JSON.stringify(team, null, 2);
  } else {
    title.textContent = 'New Team';
    textarea.value = JSON.stringify(teamsTemplate, null, 2);
  }
  textarea.focus();
}

function teamsCloseEditor() {
  document.getElementById('teamsEditor').style.display = 'none';
}

async function teamsSave() {
  const textarea = document.getElementById('teamsEditorJson');
  const hint = document.getElementById('teamsEditorHint');
  hint.textContent = '';
  hint.className = 'teams-editor-hint';

  let parsed;
  try {
    parsed = JSON.parse(textarea.value);
  } catch (e) {
    hint.textContent = 'Invalid JSON: ' + e.message;
    hint.className = 'teams-editor-hint error';
    return;
  }

  if (!parsed.name) {
    hint.textContent = 'Team name is required.';
    hint.className = 'teams-editor-hint error';
    return;
  }

  try {
    const data = await api('/api/teams', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(parsed),
    });
    if (data.ok) {
      toast('Team saved');
      teamsCloseEditor();
      await teamsFetch();
    } else {
      hint.textContent = data.error || 'Save failed';
      hint.className = 'teams-editor-hint error';
    }
  } catch (e) {
    hint.textContent = 'Save failed: ' + (e.error || e.message || 'unknown error');
    hint.className = 'teams-editor-hint error';
  }
}

async function teamsDelete(name) {
  try {
    const data = await api('/api/teams?name=' + encodeURIComponent(name), { method: 'DELETE' });
    if (data.ok) {
      toast('Team deleted');
      await teamsFetch();
    } else {
      toast(data.error || 'Delete failed', 'error');
    }
  } catch (e) {
    toast('Delete failed: ' + (e.error || e.message || 'unknown'), 'error');
  }
}

// --- Logs ---
let logsInitialized = false;

function logsStopAutoRefresh() {
  if (logsAutoTimer) { clearInterval(logsAutoTimer); logsAutoTimer = null; }
}

async function logsInit() {
  const sel = document.getElementById('logSelect');
  if (!logsInitialized) {
    logsInitialized = true;
    try {
      const res = await fetch('/api/logs');
      const data = await res.json();
      sel.innerHTML = '';
      (data.available || []).forEach(name => {
        const opt = document.createElement('option');
        opt.value = name;
        opt.textContent = name.replace(/\.log$/, '');
        sel.appendChild(opt);
      });
    } catch { sel.innerHTML = '<option>error</option>'; }
    sel.addEventListener('change', () => logsFetch());
    document.getElementById('logLines').addEventListener('change', () => logsFetch());
    document.getElementById('logRefreshBtn').addEventListener('click', () => logsFetch());
    document.getElementById('logSearch').addEventListener('input', () => logsApplyFilter());
    document.getElementById('logAutoRefresh').addEventListener('change', (e) => {
      if (e.target.checked) logsStartAutoRefresh(); else logsStopAutoRefresh();
    });
  }
  logsFetch();
  if (document.getElementById('logAutoRefresh').checked) logsStartAutoRefresh();
}

function logsStartAutoRefresh() {
  logsStopAutoRefresh();
  logsAutoTimer = setInterval(() => logsFetch(), 5000);
}

let _logsAllLines = [];
let _logsActiveFilter = null; // {type:'session'|'model'|'level', value:string}

async function logsFetch() {
  const name = document.getElementById('logSelect').value;
  const n = document.getElementById('logLines').value;
  if (!name) return;
  try {
    const res = await fetch(`/api/logs?name=${encodeURIComponent(name)}&n=${n}`);
    const data = await res.json();
    const newLines = data.lines || [];
    // Skip DOM update if lines haven't changed (avoids focus loss on auto-refresh).
    if (newLines.length === _logsAllLines.length && newLines[newLines.length - 1] === _logsAllLines[_logsAllLines.length - 1]) return;
    _logsAllLines = newLines;
    logsBuildFilters();
    logsApplyFilter();
  } catch {
    document.getElementById('logOutput').textContent = 'Failed to load logs.';
  }
}

function logsBuildFilters() {
  const container = document.getElementById('logFilters');
  if (!container) return;
  // Extract unique sessions and models from log lines.
  const sessions = new Set();
  const models = new Set();
  let hasErrors = false;
  const sidRe = /sid:([a-f0-9-]+)/i;
  const arrowRe = /→\s+([\w.-]+)\s+\d+ms/;
  _logsAllLines.forEach(line => {
    const sm = line.match(sidRe);
    if (sm) sessions.add(sm[1]);
    const mm = line.match(arrowRe);
    if (mm && mm[1] !== 'agent') models.add(mm[1]);
    if (/\bERROR\b/i.test(line)) hasErrors = true;
  });
  container.innerHTML = '';
  if (sessions.size === 0 && models.size === 0 && !hasErrors) return;

  const mkChip = (label, type, value) => {
    const btn = document.createElement('button');
    btn.className = 'log-filter-chip' + (_logsActiveFilter && _logsActiveFilter.type === type && _logsActiveFilter.value === value ? ' active' : '');
    btn.textContent = label;
    btn.onclick = () => {
      if (_logsActiveFilter && _logsActiveFilter.type === type && _logsActiveFilter.value === value) {
        _logsActiveFilter = null;
      } else {
        _logsActiveFilter = { type, value };
      }
      logsBuildFilters();
      logsApplyFilter();
    };
    container.appendChild(btn);
  };

  if (hasErrors) mkChip('Errors', 'level', 'error');
  models.forEach(m => mkChip(m, 'model', m));
  // Show last 5 sessions (most recent first).
  const sessArr = [...sessions].slice(-5).reverse();
  sessArr.forEach((s, i) => mkChip(`session ${s}`, 'session', s));
}

function logsMatchesFilter(line) {
  if (!_logsActiveFilter) return true;
  const { type, value } = _logsActiveFilter;
  if (type === 'level') return /\bERROR\b/i.test(line);
  if (type === 'session') return line.includes('sid:' + value);
  if (type === 'model') return line.includes('→ ' + value) || line.includes(value);
  return true;
}

function logsApplyFilter() {
  const q = document.getElementById('logSearch').value.toLowerCase();
  const out = document.getElementById('logOutput');
  const filtered = _logsAllLines.filter(l => {
    if (q && !l.toLowerCase().includes(q)) return false;
    return logsMatchesFilter(l);
  });
  // Preserve scroll position unless user was at the bottom (auto-follow).
  const wasAtBottom = out.scrollTop + out.clientHeight >= out.scrollHeight - 20;
  const prevScroll = out.scrollTop;
  out.innerHTML = '';
  filtered.forEach(line => {
    const span = document.createElement('span');
    span.className = 'log-line' + logsLineClass(line);
    span.textContent = line;
    out.appendChild(span);
  });
  if (wasAtBottom) {
    out.scrollTop = out.scrollHeight;
  } else {
    out.scrollTop = prevScroll;
  }
}

function logsLineClass(line) {
  if (/\bERROR\b/i.test(line)) return ' log-error';
  if (/\bWARN(ING)?\b/i.test(line)) return ' log-warn';
  if (/\bDEBUG\b/i.test(line)) return ' log-debug';
  if (/→/.test(line)) return ' log-response';
  return '';
}

// --- Tiers ---

// Populated dynamically from tiers API response (available_tools field).
let AVAILABLE_TOOLS = [];
function toolsRefresh(data) {
  if (data && data.available_tools) {
    AVAILABLE_TOOLS = data.available_tools;
  }
}

const CLI_MODELS = ['haiku', 'sonnet', 'opus'];
const EFFORTS = ['low', 'medium', 'high'];
let BACKENDS = ['', 'cli'];
// Cache of fetched models per backend name: { backendName: [{id, name}] }
const BACKEND_MODELS_CACHE = { '': CLI_MODELS.map(m => ({id: m})), 'cli': CLI_MODELS.map(m => ({id: m})) };
// Populated from tiers API response (available_backends + backend_models fields).
function backendsRefresh(data) {
  if (data && data.available_backends) {
    const names = [''];
    data.available_backends.forEach(n => names.push(n));
    BACKENDS = [...new Set(names)];
  }
  // Pre-populate model cache from backend_models (fetched at daemon startup).
  if (data && data.backend_models) {
    for (const [backend, models] of Object.entries(data.backend_models)) {
      if (models && models.length > 0) {
        // Sort models alphabetically by id.
        models.sort((a, b) => a.id.localeCompare(b.id));
        BACKEND_MODELS_CACHE[backend] = models;
      }
    }
  }
}

// Fetch models for a backend and update the model field in the form.
function fetchBackendModels(backendName, targetSelectId) {
  const key = backendName || 'cli';
  if (BACKEND_MODELS_CACHE[key]) {
    renderModelField(key, BACKEND_MODELS_CACHE[key], targetSelectId);
    return;
  }
  // Cache miss: fetch on demand (fallback if daemon cache not ready yet).
  const el = document.getElementById(targetSelectId);
  const row = el ? el.closest('.form-row') : null;
  if (row) row.innerHTML = '<label>Model</label><span class="tier-tool-desc">Loading models...</span><input type="hidden" id="' + targetSelectId + '">';
  api('/api/backends/' + encodeURIComponent(key) + '/models').then(data => {
    const models = (data.models || []).sort((a, b) => a.id.localeCompare(b.id));
    BACKEND_MODELS_CACHE[key] = models;
    renderModelField(key, models, targetSelectId);
  }).catch(() => {
    if (row) row.innerHTML = '<label>Model</label><input type="text" id="' + targetSelectId + '" placeholder="e.g. gpt-4o">';
  });
}

// Model library URLs for each backend type.
const MODEL_LIBRARY_URLS = {
  'ollama': 'https://ollama.com/library',
  'openrouter': 'https://openrouter.ai/models',
};

function modelLibraryLink(backendKey) {
  const url = MODEL_LIBRARY_URLS[backendKey];
  if (!url) return '';
  return ' <a href="' + url + '" target="_blank" rel="noopener" class="model-library-link" title="Browse models">Browse models &#8599;</a>';
}

function renderModelField(backendKey, models, targetId) {
  const el = document.getElementById(targetId);
  const row = el ? el.closest('.form-row') : null;
  if (!row) return;
  const isCLI = !backendKey || backendKey === 'cli';
  const curVal = el ? el.value : '';
  const libLink = modelLibraryLink(backendKey);
  if (models.length > 0) {
    // Check if any model has capability info (backend provides it).
    const hasToolInfo = models.some(m => m.tool_calls !== undefined && m.tool_calls !== null);
    const hasReasoningInfo = models.some(m => m.reasoning !== undefined && m.reasoning !== null);
    const hasCaps = hasToolInfo || hasReasoningInfo;

    // Sort: most capable first (tools+reasoning > tools > reasoning > unknown > none).
    const sorted = [...models];
    if (hasCaps) {
      sorted.sort((a, b) => {
        const aScore = (a.tool_calls === true ? 2 : 0) + (a.reasoning === true ? 1 : 0);
        const bScore = (b.tool_calls === true ? 2 : 0) + (b.reasoning === true ? 1 : 0);
        if (aScore !== bScore) return bScore - aScore;
        // Within same capability level: no-info before explicit-false.
        const aHas = (a.tool_calls === false || a.reasoning === false) ? 1 : 0;
        const bHas = (b.tool_calls === false || b.reasoning === false) ? 1 : 0;
        if (aHas !== bHas) return aHas - bHas;
        return a.id.localeCompare(b.id);
      });
    }

    // Build options with capability indicators.
    const opts = sorted.map(m => {
      const sel = (m.id === curVal) ? ' selected' : '';
      let label = esc(m.id);
      let badges = '';
      let cls = '';
      if (hasCaps) {
        if (m.tool_calls === true) badges += '\u{1F527}';
        if (m.reasoning === true) badges += '\u{1F9E0}';
        if (badges) label = badges + ' ' + label;
        if (m.tool_calls === false && m.reasoning === false) cls = ' class="model-opt-notools"';
      }
      return '<option value="' + esc(m.id) + '"' + sel + cls + '>' + label + '</option>';
    }).join('');
    // Add current value if not in list (user may have typed a custom model).
    const ids = sorted.map(m => m.id);
    const extra = (curVal && !ids.includes(curVal)) ? '<option value="' + esc(curVal) + '" selected>' + esc(curVal) + ' (custom)</option>' : '';
    const hasFilter = !isCLI && sorted.length > 10;
    var legendParts = [];
    if (hasToolInfo) legendParts.push('\u{1F527} tools');
    if (hasReasoningInfo) legendParts.push('\u{1F9E0} reasoning');
    const legend = legendParts.length ? '<span class="model-legend">' + legendParts.join(' &nbsp; ') + '</span>' : '';
    row.innerHTML = '<label>Model' + libLink + '</label>' + legend + '<select id="' + targetId + '">' + extra + opts + '</select>' +
      (hasFilter ? '<input type="text" class="model-filter" placeholder="Filter models...">' : '');
    if (hasFilter) {
      const filterInput = row.querySelector('.model-filter');
      const select = document.getElementById(targetId);
      if (filterInput && select) {
        filterInput.addEventListener('input', function() {
          const q = this.value.toLowerCase();
          Array.from(select.options).forEach(opt => {
            opt.style.display = opt.text.toLowerCase().includes(q) || !q ? '' : 'none';
          });
        });
      }
    }
  } else {
    row.innerHTML = '<label>Model' + libLink + '</label><input type="text" id="' + targetId + '" value="' + esc(curVal) + '" placeholder="e.g. anthropic/claude-haiku-4-5">';
  }
}

let tiersCache = null;
let tiersInitialized = false;

function tiersInit() {
  if (!tiersInitialized) {
    tiersInitialized = true;
    document.getElementById('tiersAddBtn').addEventListener('click', () => tiersShowModal(null));
    document.getElementById('tiersConfigSelect').addEventListener('change', tiersConfigSwitch);
    document.getElementById('tiersDuplicateBtn').addEventListener('click', tiersConfigDuplicate);
  }
  tiersLoad();
  tiersConfigsLoad();
}

function tiersLoad() {
  api('/api/tiers').then(data => {
    backendsRefresh(data);
    toolsRefresh(data);
    tiersCache = data;
    tiersRender();
  }).catch(() => toast('Failed to load tiers', 'error'));
}

async function tiersConfigsLoad() {
  const select = document.getElementById('tiersConfigSelect');
  try {
    const configs = await api('/api/tiers/configs');
    if (!configs || configs.length === 0) {
      select.style.display = 'none';
      return;
    }
    select.style.display = '';
    select.innerHTML = configs.map(c =>
      '<option value="' + esc(c.name) + '.json"' + (c.active ? ' selected' : '') + '>' +
        esc(c.name) + ' (' + c.tiers + ' tiers)' +
      '</option>'
    ).join('');
  } catch (e) {
    select.style.display = 'none';
  }
}

async function tiersConfigSwitch() {
  const select = document.getElementById('tiersConfigSelect');
  const name = select.value;
  if (!name) return;
  try {
    await api('/api/tiers/configs/switch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name })
    });
    toast('Switched to ' + name.replace('.json', ''));
    tiersLoad();
    tiersConfigsLoad();
  } catch (err) {
    alert('Switch failed: ' + (err?.error || err?.message || 'unknown'));
    tiersConfigsLoad();
  }
}

async function tiersConfigDuplicate() {
  const select = document.getElementById('tiersConfigSelect');
  const current = select.value;
  if (!current) return;
  const baseName = current.replace('.json', '');
  const newName = prompt('New config name:', baseName + '-copy');
  if (!newName || !newName.trim()) return;
  const safeName = newName.trim().replace(/[^a-zA-Z0-9_-]/g, '-');
  try {
    await api('/api/tiers/configs/duplicate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source: current, name: safeName + '.json' })
    });
    toast('Duplicated as ' + safeName);
    tiersConfigsLoad();
  } catch (err) {
    toast('Duplicate failed: ' + (err?.error || err?.message || 'unknown'), 'error');
  }
}

function tiersRender() {
  if (!tiersCache) return;
  const list = document.getElementById('tiersList');
  const cfg = document.getElementById('tiersRouterConfig');

  // Router config summary
  const routerBackendLabel = tiersCache.router_backend && tiersCache.router_backend !== 'cli' ? tiersCache.router_backend : 'cli';
  cfg.innerHTML = '<div class="tiers-router-card">' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Router backend</span><span class="tiers-router-value">' + esc(routerBackendLabel) + '</span></div>' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Router model</span><span class="tiers-router-value">' + esc(tiersCache.router_model || 'haiku') + '</span></div>' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Default fallback</span><span class="tiers-router-value">' + esc(tiersCache.default_fallback || 'haiku') + '</span></div>' +
    '<div class="tiers-router-row"><button class="btn-sm" id="tiersEditRouterBtn">Edit router settings</button></div>' +
    '</div>';
  document.getElementById('tiersEditRouterBtn').addEventListener('click', () => tiersShowRouterModal());

  // Memory config summary
  const mem = tiersCache.memory || {};
  const embUrl = (mem.embedding && mem.embedding.url) || (tiersCache.embedding && tiersCache.embedding.url) || '';
  const extractBackend = mem.extract_backend || 'auto';
  const extractModel = mem.extract_model || '';
  const memCfg = document.getElementById('tiersMemoryConfig');
  if (memCfg) {
    memCfg.innerHTML = '<div class="tiers-router-card">' +
      '<div class="tiers-router-row"><span class="tiers-router-label">Extraction backend</span><span class="tiers-router-value">' + esc(extractBackend) + '</span></div>' +
      (extractModel ? '<div class="tiers-router-row"><span class="tiers-router-label">Extraction model</span><span class="tiers-router-value">' + esc(extractModel) + '</span></div>' : '') +
      '<div class="tiers-router-row"><span class="tiers-router-label">Embedding URL</span><span class="tiers-router-value">' + esc(embUrl || 'none') + '</span></div>' +
      '<div class="tiers-router-row"><button class="btn-sm" id="tiersEditMemoryBtn">Edit memory settings</button></div>' +
      '</div>';
    document.getElementById('tiersEditMemoryBtn').addEventListener('click', () => tiersShowMemoryModal());
  }

  // Tier cards
  const tiers = tiersCache.tiers || [];
  if (!tiers.length) {
    list.innerHTML = '<div class="task-empty"><div class="task-empty-icon">&#9881;</div>No tiers configured</div>';
    return;
  }

  list.innerHTML = tiers.map((t, i) => {
    const modelColor = t.model === 'opus' ? 'var(--accent)' : t.model === 'sonnet' ? 'var(--green)' : 'var(--text-dim)';
    const statusDot = t.enabled ? '<span class="dot green"></span>' : '<span class="dot red"></span>';
    const badges = [];
    if (t.write_capable) badges.push('<span class="tier-badge tier-badge-write">write</span>');
    if (t.force_command) badges.push('<span class="tier-badge tier-badge-force">force</span>');
    if (t.routable) badges.push('<span class="tier-badge tier-badge-routable">routable</span>');
    if (t.backend && t.backend !== 'cli') badges.push('<span class="tier-badge" style="background:rgba(139,92,246,0.15);color:#a78bfa">' + esc(t.backend) + '</span>');
    const toolsList = t.tools || [];
    const tools = toolsList.includes('*') ? 'all (wildcard)' : toolsList.join(', ') || (t.write_capable ? 'all (write-capable)' : 'none');
    return '<div class="tier-card" data-idx="' + i + '">' +
      '<div class="tier-card-header">' +
        '<div class="tier-card-title">' + statusDot + '<strong>' + esc(t.name) + '</strong></div>' +
        '<span class="tier-model-badge" style="color:' + modelColor + '">' + esc(t.model) + '</span>' +
        '<span class="tier-priority">P' + t.priority + '</span>' +
        '<div class="tier-card-actions">' +
          '<button class="btn-sm tier-edit-btn" data-idx="' + i + '">Edit</button>' +
          '<button class="btn-sm btn-danger tier-delete-btn" data-idx="' + i + '">Delete</button>' +
        '</div>' +
      '</div>' +
      '<div class="tier-card-details">' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Label</span><span class="tier-detail-value">' + esc(t.router_label || t.description || '-') + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Tools</span><span class="tier-detail-value">' + esc(tools) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Effort</span><span class="tier-detail-value">' + esc(t.effort || '-') + '</span></div>' +
        (t.max_turns ? '<div class="tier-detail-row"><span class="tier-detail-label">Max turns</span><span class="tier-detail-value">' + t.max_turns + '</span></div>' : '') +
        (t.max_iterations ? '<div class="tier-detail-row"><span class="tier-detail-label">Max iterations</span><span class="tier-detail-value">' + t.max_iterations + '</span></div>' : '') +
        (t.timeout_minutes ? '<div class="tier-detail-row"><span class="tier-detail-label">Timeout</span><span class="tier-detail-value">' + t.timeout_minutes + ' min</span></div>' : '') +
        (badges.length ? '<div class="tier-detail-row"><span class="tier-detail-label">Flags</span><span class="tier-detail-value">' + badges.join(' ') + '</span></div>' : '') +
      '</div>' +
    '</div>';
  }).join('');

  list.querySelectorAll('.tier-edit-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      tiersShowModal(tiersCache.tiers[+btn.dataset.idx]);
    });
  });
  list.querySelectorAll('.tier-delete-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const idx = +btn.dataset.idx;
      const name = tiersCache.tiers[idx].name;
      if (!confirm('Delete tier "' + name + '"?')) return;
      tiersCache.tiers.splice(idx, 1);
      tiersSave();
    });
  });
}

function tiersShowModal(tier) {
  const isEdit = !!tier;
  const t = tier || { name: '', model: 'haiku', priority: 0, enabled: true, routable: true, router_label: '', effort: 'low', tools: [], write_capable: false, force_command: false, max_turns: 0, max_iterations: 0, timeout_minutes: 0, description: '', backend: '' };

  // Remove existing modal
  const old = document.getElementById('tierModal');
  if (old) old.remove();

  const isWildcard = (t.tools || []).includes('*');
  const cliToolsList = AVAILABLE_TOOLS.filter(tool => tool.source === 'cli');
  const alfToolsList = AVAILABLE_TOOLS.filter(tool => tool.source === 'alf');
  const wildcardCheck = '<label class="tier-tool-check tier-tool-wildcard"><input type="checkbox" id="tfToolsWildcard" value="*"' + (isWildcard ? ' checked' : '') + '> <strong>* (all tools)</strong> <span class="tier-tool-desc">-enable all available tools</span></label>';
  const cliChecks = cliToolsList.map(tool => {
    const checked = !isWildcard && (t.tools || []).includes(tool.name) ? ' checked' : '';
    return '<label class="tier-tool-check tier-tool-cli"><input type="checkbox" value="' + tool.name + '"' + checked + (isWildcard ? ' disabled' : '') + '> <strong>' + tool.name + '</strong> <span class="tier-tool-desc">-' + esc(tool.desc) + '</span></label>';
  }).join('');
  const alfChecks = alfToolsList.length ? alfToolsList.map(tool => {
    const checked = !isWildcard && (t.tools || []).includes(tool.name) ? ' checked' : '';
    return '<label class="tier-tool-check tier-tool-alf"><input type="checkbox" value="' + tool.name + '"' + checked + (isWildcard ? ' disabled' : '') + '> <strong>' + tool.name + '</strong> <span class="tier-tool-desc">-' + esc(tool.desc) + '</span></label>';
  }).join('') : '';
  const toolChecks = wildcardCheck +
    (cliChecks ? '<div class="tier-tools-group-label">CLI tools</div>' + cliChecks : '') +
    (alfChecks ? '<div class="tier-tools-group-label">ALF tools</div>' + alfChecks : '');

  const effortOpts = ['', ...EFFORTS].map(e => '<option value="' + e + '"' + (t.effort === e ? ' selected' : '') + '>' + (e || '-') + '</option>').join('');
  const backendOpts = BACKENDS.map(b => '<option value="' + b + '"' + ((t.backend || '') === b ? ' selected' : '') + '>' + (b || 'claude (default)') + '</option>').join('');
  // Initial model field: temporary placeholder, will be populated by fetchBackendModels after modal renders
  const modelPlaceholder = '<input type="text" id="tfModel" value="' + esc(t.model) + '" placeholder="Loading...">';

  const html = '<div class="modal-backdrop" id="tierModal">' +
    '<div class="modal tier-modal">' +
      '<h3>' + (isEdit ? 'Edit Tier' : 'Add Tier') + '</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Name</label><input type="text" id="tfName" value="' + esc(t.name) + '"' + (isEdit ? ' readonly style="opacity:0.6"' : '') + '></div>' +
        '<div class="form-row"><label>Backend</label><select id="tfBackend">' + backendOpts + '</select></div>' +
        '<div class="form-row" id="tfModelRow"><label>Model</label>' + modelPlaceholder + '</div>' +
        '<div class="form-row"><label>Priority</label><input type="number" id="tfPriority" value="' + t.priority + '" min="0" max="99"></div>' +
        '<div class="form-row" id="tfEffortRow"><label>Effort</label><select id="tfEffort">' + effortOpts + '</select></div>' +
        '<div class="form-row"><label>Router label</label><textarea id="tfLabel" class="input tier-label-textarea" rows="2" placeholder="Description for the router">' + esc(t.router_label || '') + '</textarea></div>' +
        '<div class="form-row"><label>Description</label><input type="text" id="tfDesc" value="' + esc(t.description || '') + '" placeholder="Optional description"></div>' +
        '<div class="form-row"><label>Max turns</label><input type="number" id="tfMaxTurns" value="' + (t.max_turns || 0) + '" min="0"></div>' +
        '<div class="form-row"><label>Max iterations</label><input type="number" id="tfMaxIter" value="' + (t.max_iterations || 0) + '" min="0"></div>' +
        '<div class="form-row"><label>Timeout (min)</label><input type="number" id="tfTimeout" value="' + (t.timeout_minutes || 0) + '" min="0"></div>' +
        '<div class="tier-flags">' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfEnabled"' + (t.enabled ? ' checked' : '') + '> Enabled</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfRoutable"' + (t.routable ? ' checked' : '') + '> Routable</label>' +
          '<label class="tier-flag-check" id="tfWriteCapableLabel"><input type="checkbox" id="tfWriteCapable"' + (t.write_capable ? ' checked' : '') + '> Write capable</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfForceCmd"' + (t.force_command ? ' checked' : '') + '> Force command</label>' +
        '</div>' +
        '<div class="form-row"><label>System prompt</label><textarea id="tfSystemPrompt" class="input tier-label-textarea" rows="3" placeholder="Extra instructions prepended for this tier (optional)">' + esc(t.system_prompt || '') + '</textarea></div>' +
        '<div class="tier-tools-section">' +
          '<div class="tier-tools-header">Tools <span class="tier-tools-hint">(CLI tools for Claude tiers, ALF tools for API tiers with tool loop)</span></div>' +
          '<div class="tier-tools-list" id="tfTools">' + toolChecks + '</div>' +
        '</div>' +
      '</div>' +
      '<div class="upload-actions">' +
        '<button class="btn" id="tierModalCancel">Cancel</button>' +
        '<button class="btn btn-primary" id="tierModalSave">' + (isEdit ? 'Save' : 'Add') + '</button>' +
      '</div>' +
    '</div>' +
  '</div>';

  document.body.insertAdjacentHTML('beforeend', html);

  // Fetch and render models for the current backend
  fetchBackendModels(t.backend || '', 'tfModel');

  // Toggle tools visibility based on write_capable
  const wcCheck = document.getElementById('tfWriteCapable');
  const toolsSection = document.querySelector('.tier-tools-section');
  function toggleTools() { toolsSection.style.opacity = wcCheck.checked ? '0.4' : '1'; }
  toggleTools();
  wcCheck.addEventListener('change', toggleTools);

  // Wildcard toggle: disable individual tool checkboxes when * is checked.
  const wildcardCb = document.getElementById('tfToolsWildcard');
  function toggleWildcard() {
    const indivCbs = document.querySelectorAll('#tfTools input[type=checkbox]:not(#tfToolsWildcard)');
    indivCbs.forEach(cb => {
      cb.disabled = wildcardCb.checked;
      if (wildcardCb.checked) cb.checked = false;
    });
  }
  if (wildcardCb) wildcardCb.addEventListener('change', toggleWildcard);

  // Swap model select/input when backend changes + toggle CLI tools visibility
  function updateToolsVisibility(backendVal) {
    const isCLI = !backendVal || backendVal === 'cli';
    document.querySelectorAll('.tier-tool-cli').forEach(el => el.style.display = isCLI ? '' : 'none');
    const cliGroupLabel = document.querySelector('.tier-tools-group-label');
    if (cliGroupLabel && cliGroupLabel.textContent === 'CLI tools') cliGroupLabel.style.display = isCLI ? '' : 'none';
    const hint = document.querySelector('.tier-tools-hint');
    if (hint) hint.textContent = isCLI ? 'CLI tools for Claude tiers' : 'ALF tools for API tiers with tool loop';
    const wcLabel = document.getElementById('tfWriteCapableLabel');
    if (wcLabel) wcLabel.style.display = isCLI ? '' : 'none';
    // Effort is available for all backends (CLI: --effort flag, API: reasoning.effort).
    // Always visible.
  }
  updateToolsVisibility(t.backend);

  document.getElementById('tfBackend').addEventListener('change', function() {
    fetchBackendModels(this.value, 'tfModel');
    updateToolsVisibility(this.value);
  });

  document.getElementById('tierModalCancel').addEventListener('click', () => document.getElementById('tierModal').remove());
  document.getElementById('tierModal').addEventListener('click', e => { if (e.target.id === 'tierModal') document.getElementById('tierModal').remove(); });

  document.getElementById('tierModalSave').addEventListener('click', () => {
    const backend = document.getElementById('tfBackend').value;
    const newTier = {
      name: document.getElementById('tfName').value.trim(),
      model: document.getElementById('tfModel').value.trim(),
      priority: parseInt(document.getElementById('tfPriority').value, 10) || 0,
      enabled: document.getElementById('tfEnabled').checked,
      routable: document.getElementById('tfRoutable').checked,
      router_label: document.getElementById('tfLabel').value.trim(),
      description: document.getElementById('tfDesc').value.trim(),
      max_turns: parseInt(document.getElementById('tfMaxTurns').value, 10) || 0,
      max_iterations: parseInt(document.getElementById('tfMaxIter').value, 10) || 0,
      timeout_minutes: parseInt(document.getElementById('tfTimeout').value, 10) || 0,
      effort: document.getElementById('tfEffort').value,
      write_capable: document.getElementById('tfWriteCapable').checked,
      force_command: document.getElementById('tfForceCmd').checked,
      backend: backend,
      system_prompt: document.getElementById('tfSystemPrompt').value.trim(),
      tools: [],
    };
    if (!newTier.write_capable) {
      const wc = document.getElementById('tfToolsWildcard');
      if (wc && wc.checked) {
        newTier.tools = ['*'];
      } else {
        document.querySelectorAll('#tfTools input:checked:not(#tfToolsWildcard)').forEach(cb => newTier.tools.push(cb.value));
      }
    }
    // Clean zero values
    if (!newTier.max_turns) delete newTier.max_turns;
    if (!newTier.max_iterations) delete newTier.max_iterations;
    if (!newTier.timeout_minutes) delete newTier.timeout_minutes;
    if (!newTier.router_label) delete newTier.router_label;
    if (!newTier.description) delete newTier.description;
    if (!newTier.effort) delete newTier.effort;
    if (!newTier.tools || !newTier.tools.length) delete newTier.tools;
    if (!newTier.backend || newTier.backend === 'cli') delete newTier.backend;

    if (!newTier.name) { toast('Name is required', 'error'); return; }

    if (isEdit) {
      const idx = tiersCache.tiers.findIndex(x => x.name === tier.name);
      if (idx >= 0) tiersCache.tiers[idx] = newTier;
    } else {
      if (tiersCache.tiers.some(x => x.name === newTier.name)) { toast('Tier "' + newTier.name + '" already exists', 'error'); return; }
      tiersCache.tiers.push(newTier);
    }
    document.getElementById('tierModal').remove();
    tiersSave();
  });
}

function tiersShowRouterModal() {
  const old = document.getElementById('tierRouterModal');
  if (old) old.remove();

  const c = tiersCache;
  const fbOpts = (c.tiers || []).map(t => '<option value="' + t.name + '"' + (c.default_fallback === t.name ? ' selected' : '') + '>' + t.name + '</option>').join('');
  const rbOpts = BACKENDS.map(b => '<option value="' + b + '"' + ((c.router_backend || '') === b ? ' selected' : '') + '>' + (b || 'claude (default)') + '</option>').join('');
  const modelPlaceholder = '<input type="text" id="trModel" value="' + esc(c.router_model || '') + '" placeholder="Loading...">';

  const html = '<div class="modal-backdrop" id="tierRouterModal">' +
    '<div class="modal tier-modal">' +
      '<h3>Router Settings</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Router backend</label><select id="trBackend">' + rbOpts + '</select></div>' +
        '<div class="form-row" id="trModelRow"><label>Router model</label>' + modelPlaceholder + '</div>' +
        '<div class="form-row"><label>Default fallback</label><select id="trFallback">' + fbOpts + '</select></div>' +
        '<div class="form-row"><label>Distinctions</label><textarea class="json-editor" id="trDistinctions" rows="4">' + esc(c.router_distinctions || '') + '</textarea></div>' +
      '</div>' +
      '<div class="upload-actions">' +
        '<button class="btn" id="trCancel">Cancel</button>' +
        '<button class="btn btn-primary" id="trSave">Save</button>' +
      '</div>' +
    '</div>' +
  '</div>';

  document.body.insertAdjacentHTML('beforeend', html);

  // Fetch and render models for the current router backend
  fetchBackendModels(c.router_backend || '', 'trModel');

  // Swap model dropdown when backend changes
  document.getElementById('trBackend').addEventListener('change', function() {
    fetchBackendModels(this.value, 'trModel');
  });

  document.getElementById('trCancel').addEventListener('click', () => document.getElementById('tierRouterModal').remove());
  document.getElementById('tierRouterModal').addEventListener('click', e => { if (e.target.id === 'tierRouterModal') document.getElementById('tierRouterModal').remove(); });

  document.getElementById('trSave').addEventListener('click', () => {
    const backend = document.getElementById('trBackend').value;
    tiersCache.router_backend = backend || '';
    tiersCache.router_model = document.getElementById('trModel').value.trim();
    tiersCache.default_fallback = document.getElementById('trFallback').value;
    tiersCache.router_distinctions = document.getElementById('trDistinctions').value.trim();
    document.getElementById('tierRouterModal').remove();
    tiersSave();
  });
}

function tiersShowMemoryModal() {
  const old = document.getElementById('tierMemoryModal');
  if (old) old.remove();

  const mem = tiersCache.memory || {};
  const emb = (mem.embedding) || (tiersCache.embedding) || {};
  const backendOpts = ['', 'cli', ...BACKENDS.filter(b => b && b !== 'cli')].map(b => {
    const label = !b ? 'auto (prefer API, fallback CLI)' : b;
    const sel = (mem.extract_backend || '') === b ? ' selected' : '';
    return '<option value="' + b + '"' + sel + '>' + label + '</option>';
  }).join('');

  const html = '<div class="modal-backdrop" id="tierMemoryModal">' +
    '<div class="modal tier-modal">' +
      '<h3>Memory Settings</h3>' +
      '<div class="tier-form">' +
        '<h4 style="margin:0 0 8px;font-size:0.82rem;color:var(--text-dim)">Extraction (LLM)</h4>' +
        '<div class="form-row"><label>Backend</label><select id="tmExtractBackend">' + backendOpts + '</select></div>' +
        '<div class="form-row"><label>Model override</label><input type="text" id="tmExtractModel" value="' + esc(mem.extract_model || '') + '" placeholder="default (from extractor)"></div>' +
        '<h4 style="margin:12px 0 8px;font-size:0.82rem;color:var(--text-dim)">Embedding (vectors)</h4>' +
        '<div class="form-row"><label>Embed service URL</label><input type="text" id="tmEmbedUrl" value="' + esc(emb.url || '') + '" placeholder="http://embed:8090 or empty to disable"></div>' +
        '<div class="form-row"><label>Embed model</label><input type="text" id="tmEmbedModel" value="' + esc(emb.model || '') + '" placeholder="default"></div>' +
      '</div>' +
      '<div class="upload-actions">' +
        '<button class="btn" id="tmCancel">Cancel</button>' +
        '<button class="btn btn-primary" id="tmSave">Save</button>' +
      '</div>' +
    '</div>' +
  '</div>';

  document.body.insertAdjacentHTML('beforeend', html);

  document.getElementById('tmCancel').addEventListener('click', () => document.getElementById('tierMemoryModal').remove());
  document.getElementById('tierMemoryModal').addEventListener('click', e => { if (e.target.id === 'tierMemoryModal') document.getElementById('tierMemoryModal').remove(); });

  document.getElementById('tmSave').addEventListener('click', () => {
    const extractBackend = document.getElementById('tmExtractBackend').value;
    const extractModel = document.getElementById('tmExtractModel').value.trim();
    const embedUrl = document.getElementById('tmEmbedUrl').value.trim();
    const embedModel = document.getElementById('tmEmbedModel').value.trim();

    // Build memory config.
    var mem = {};
    if (extractBackend) mem.extract_backend = extractBackend;
    if (extractModel) mem.extract_model = extractModel;
    if (embedUrl || embedModel) {
      mem.embedding = {};
      if (embedUrl) mem.embedding.url = embedUrl;
      if (embedModel) mem.embedding.model = embedModel;
    }
    tiersCache.memory = Object.keys(mem).length ? mem : undefined;
    // Clear legacy embedding if migrated to memory.embedding.
    if (mem.embedding) delete tiersCache.embedding;

    document.getElementById('tierMemoryModal').remove();
    tiersSave();
  });
}

function tiersSave() {
  api('/api/tiers', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(tiersCache),
  }).then(() => {
    toast('Tiers saved');
    tiersLoad();
    tiersConfigsLoad();
  }).catch(err => {
    toast('Save failed: ' + (err.error || err.message || 'unknown'), 'error');
    tiersLoad();
    tiersConfigsLoad();
  });
}

// Hoist auto-refresh timers so navigateTo can safely call stop* functions.
var tasksAutoTimer = null, logsAutoTimer = null, fwAutoTimer = null;

// --- Init ---
const savedView = localStorage.getItem('alf-view');
navigateTo(savedView || 'chat');
lucide.createIcons();
document.body.classList.add('app-ready');

// --- Welcome / Setup Wizard ---
if (!localStorage.getItem('alf-welcomed')) {
  api('/api/setup/status').then(status => {
    if (!status.completed) {
      showSetupWizard(status);
    } else {
      showWelcomeModal();
    }
  }).catch(() => showWelcomeModal());
}

function showWelcomeModal() {
  const wb = document.createElement('div');
  wb.className = 'welcome-backdrop';
  wb.innerHTML = '<div class="welcome-modal">' +
    '<div class="welcome-logo">ALF</div>' +
    '<h2>Welcome to your Control Center</h2>' +
    '<p>This is your personal command hub -manage schedules, agent teams, tools, and more from here.</p>' +
    '<div class="welcome-steps">' +
      '<div class="welcome-step"><span class="welcome-step-num">1</span><span><strong>Say hello in the Chat</strong> -ALF will learn about you through a short onboarding conversation.</span></div>' +
      '<div class="welcome-step"><span class="welcome-step-num">2</span><span><strong>Explore the Docs</strong> -check the getting started guide to discover what ALF can do.</span></div>' +
      '<div class="welcome-step"><span class="welcome-step-num">3</span><span><strong>Make it yours</strong> -configure tiers, add skills, and connect your services in the Vault.</span></div>' +
    '</div>' +
    '<button class="btn btn-primary welcome-cta" id="welcomeStartBtn">Get started</button>' +
  '</div>';
  document.body.appendChild(wb);
  document.getElementById('welcomeStartBtn').addEventListener('click', () => {
    localStorage.setItem('alf-welcomed', '1');
    wb.classList.add('welcome-closing');
    setTimeout(() => wb.remove(), 300);
    navigateTo('chat');
  });
}

function showSetupWizard(status) {
  const steps = ['Backend', 'Telegram', 'Tiers', 'Done'];
  let current = 0;
  const state = { backends: {}, telegram: null, presetId: '', vaultPassword: '' };

  // Build modal
  const wb = document.createElement('div');
  wb.className = 'welcome-backdrop';

  let stepperHTML = '<div class="setup-stepper">';
  steps.forEach((s, i) => {
    if (i > 0) stepperHTML += '<div class="setup-step-line" data-line="' + i + '"></div>';
    stepperHTML += '<div class="setup-step-item"><div class="setup-step-dot" data-dot="' + i + '">' + (i + 1) + '</div></div>';
  });
  stepperHTML += '</div>';

  wb.innerHTML = '<div class="welcome-modal setup-wizard-modal">' +
    '<div class="welcome-logo">ALF</div>' +
    '<h2>Setup Wizard</h2>' +
    stepperHTML +
    '<div class="setup-step-content" data-step="0"></div>' +
    '<div class="setup-step-content" data-step="1"></div>' +
    '<div class="setup-step-content" data-step="2"></div>' +
    '<div class="setup-step-content" data-step="3"></div>' +
    '<div class="setup-nav">' +
      '<button class="btn btn-secondary" id="setupPrev" style="visibility:hidden">Back</button>' +
      '<div class="setup-nav-right">' +
        '<button class="btn btn-secondary" id="setupSkip" style="display:none">Skip</button>' +
        '<button class="btn btn-primary" id="setupNext">Next</button>' +
      '</div>' +
    '</div>' +
  '</div>';
  document.body.appendChild(wb);

  const modal = wb.querySelector('.setup-wizard-modal');
  const prevBtn = document.getElementById('setupPrev');
  const nextBtn = document.getElementById('setupNext');
  const skipBtn = document.getElementById('setupSkip');

  function goTo(step) {
    current = step;
    // Update stepper
    modal.querySelectorAll('.setup-step-dot').forEach((d, i) => {
      d.className = 'setup-step-dot' + (i === current ? ' active' : i < current ? ' done' : '');
    });
    modal.querySelectorAll('.setup-step-line').forEach((l, i) => {
      l.className = 'setup-step-line' + (i < current ? ' done' : '');
    });
    // Show current step
    modal.querySelectorAll('.setup-step-content').forEach((c, i) => {
      c.className = 'setup-step-content' + (i === current ? ' active' : '');
    });
    // Navigation
    prevBtn.style.visibility = current === 0 ? 'hidden' : 'visible';
    skipBtn.style.display = current === 1 ? '' : 'none'; // only Telegram is skippable
    if (current === steps.length - 1) {
      nextBtn.textContent = 'Apply & Start';
      renderDone();
    } else {
      nextBtn.textContent = 'Next';
    }
    // Reset tiers when navigating backward (backends may have changed)
    if (current < 2) tiersRendered = false;
    // Init step content
    if (current === 0) renderBackend();
    if (current === 1) renderTelegram();
    if (current === 2) renderTiers();
  }

  prevBtn.addEventListener('click', () => {
    if (backendConfigPhase && current === 0) {
      // Go back from config phase to selection phase.
      backendConfigPhase = false;
      backendRendered = false;
      renderBackend();
      prevBtn.style.visibility = 'hidden';
    } else if (current > 0) {
      goTo(current - 1);
    }
  });
  skipBtn.addEventListener('click', () => { state.telegram = null; goTo(current + 1); });
  nextBtn.addEventListener('click', () => {
    if (current === 0 && !backendConfigPhase && selectedBackendsNeedConfig()) {
      // Show config sub-step before advancing.
      renderBackendConfig();
      prevBtn.style.visibility = 'visible';
      return;
    }
    if (current === 0 && backendConfigPhase) {
      backendConfigPhase = false;
    }
    if (current < steps.length - 1) {
      goTo(current + 1);
    } else {
      applySetup();
    }
  });

  // --- Step 0: Backend Selection (pick which backends) ---
  const backendDefs = [
    { id: 'claude', name: 'Claude', desc: 'Anthropic via local CLI', fields: [] },
    { id: 'codex', name: 'OpenAI Codex', desc: 'OpenAI via local CLI', fields: [
      { key: 'api_key', label: 'API Key (optional)', type: 'password', placeholder: 'sk-... or leave empty for codex login' }
    ]},
    { id: 'openrouter', name: 'OpenRouter', desc: 'Multi-model gateway', fields: [
      { key: 'api_key', label: 'API Key', type: 'password', placeholder: 'sk-or-...' }
    ]},
    { id: 'openai', name: 'OpenAI', desc: 'GPT models', fields: [
      { key: 'base_url', label: 'Base URL', type: 'text', placeholder: 'https://api.openai.com/v1', defaultVal: 'https://api.openai.com/v1' },
      { key: 'api_key', label: 'API Key', type: 'password', placeholder: 'sk-...' }
    ]},
    { id: 'ollama', name: 'Ollama', desc: 'Local models', fields: [
      { key: 'base_url', label: 'Base URL', type: 'text', placeholder: 'http://host.docker.internal:11434/v1', defaultVal: 'http://host.docker.internal:11434/v1' }
    ]},
    { id: 'custom', name: 'Custom', desc: 'OpenAI-compatible endpoint', fields: [
      { key: 'base_url', label: 'Base URL', type: 'text', placeholder: 'https://...' },
      { key: 'api_key', label: 'API Key', type: 'password', placeholder: 'sk-...' },
      { key: 'default_model', label: 'Default model', type: 'text', placeholder: 'model-name' }
    ]}
  ];

  let backendRendered = false;
  function renderBackend() {
    if (backendRendered) return;
    backendRendered = true;
    const el = modal.querySelector('[data-step="0"]');

    let html = '<p style="font-size:0.82rem;color:var(--text-dim);margin:0 0 12px">Select one or more LLM backends to connect.</p>';
    html += '<div class="setup-backend-grid">';
    backendDefs.forEach(b => {
      html += '<div class="setup-backend-card" data-backend="' + b.id + '">' +
        '<h4>' + b.name + '</h4><p>' + b.desc + '</p>';
      if (b.id === 'claude') {
        html += '<div class="setup-claude-status pending" id="setupClaudeStatus">Checking...</div>';
      }
      if (b.id === 'codex') {
        html += '<div class="setup-claude-status pending" id="setupCodexStatus">Checking...</div>';
      }
      html += '</div>';
    });
    html += '</div>';
    el.innerHTML = html;

    // Card selection toggle
    el.querySelectorAll('.setup-backend-card').forEach(card => {
      card.addEventListener('click', () => {
        card.classList.toggle('selected');
        const bid = card.dataset.backend;
        if (!card.classList.contains('selected')) {
          delete state.backends[bid];
        } else {
          state.backends[bid] = {};
          if (bid === 'openrouter') state.backends[bid].base_url = 'https://openrouter.ai/api/v1';
        }
      });
    });

    // Check Claude auth
    api('/api/setup/claude/check').then(r => {
      const cs = document.getElementById('setupClaudeStatus');
      if (cs) {
        if (r.authenticated) {
          cs.textContent = 'Authenticated';
          cs.className = 'setup-claude-status ok';
        } else {
          cs.innerHTML = 'Not authenticated — open the Terminal tab, type <code>claude</code>, then run <code>/login</code>. Type <code>/exit</code> when done.';
          cs.className = 'setup-claude-status pending';
        }
      }
    }).catch(() => {});

    // Codex auth hint
    const cxs = document.getElementById('setupCodexStatus');
    if (cxs) {
      cxs.innerHTML = 'API key <em>or</em> <code>codex login --device-auth</code> in Terminal';
      cxs.className = 'setup-claude-status pending';
    }
  }

  // --- Step 0b: Backend Config (configure selected backends) ---
  // This is an internal sub-step that reuses step-content[data-step="0"].
  let backendConfigPhase = false;

  function selectedBackendsNeedConfig() {
    return Object.keys(state.backends).some(bid => {
      const def = backendDefs.find(d => d.id === bid);
      return def && def.fields.length > 0;
    });
  }

  function renderBackendConfig() {
    backendConfigPhase = true;
    const el = modal.querySelector('[data-step="0"]');
    const selected = Object.keys(state.backends);

    let html = '<p style="font-size:0.82rem;color:var(--text-dim);margin:0 0 12px">Configure your selected backends.</p>';
    selected.forEach(bid => {
      const def = backendDefs.find(d => d.id === bid);
      if (!def || def.fields.length === 0) return;
      html += '<div class="setup-config-section" data-config="' + bid + '">';
      html += '<h4>' + def.name + '</h4>';
      def.fields.forEach(f => {
        const val = (state.backends[bid] && state.backends[bid][f.key]) || f.defaultVal || '';
        html += '<div class="form-group"><label>' + f.label + '</label>' +
          '<input type="' + f.type + '" class="input" data-field="' + f.key + '" placeholder="' + f.placeholder + '" value="' + val + '"></div>';
      });
      html += '<div class="test-row"><button class="btn btn-sm" data-test="' + bid + '">Test</button><span class="test-result" data-result="' + bid + '"></span></div>';
      if (bid === 'ollama') {
        html += '<div class="setup-ollama-models" id="setupOllamaModels"></div>';
      }
      html += '</div>';
    });
    el.innerHTML = html;

    // Collect on input change
    el.querySelectorAll('.setup-config-section').forEach(section => {
      const bid = section.dataset.config;
      section.querySelectorAll('input').forEach(inp => {
        inp.addEventListener('input', () => collectConfigFields(section, bid));
      });
    });

    // Test buttons
    el.querySelectorAll('[data-test]').forEach(btn => {
      btn.addEventListener('click', async (e) => {
        e.stopPropagation();
        const bid = btn.dataset.test;
        const section = btn.closest('.setup-config-section');
        const result = section.querySelector('[data-result="' + bid + '"]');
        result.textContent = 'Testing...';
        result.className = 'test-result';
        collectConfigFields(section, bid);
        try {
          const body = { type: bid };
          if (state.backends[bid]) {
            body.base_url = state.backends[bid].base_url || '';
            body.api_key = state.backends[bid].api_key || '';
          }
          const res = await api('/api/setup/backend/test', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body)
          });
          result.textContent = res.ok ? 'Connected' : (res.error || 'Failed');
          result.className = 'test-result ' + (res.ok ? 'ok' : 'fail');
          if (bid === 'ollama' && res.ok) loadOllamaModels();
        } catch (err) {
          result.textContent = err.error || 'Connection failed';
          result.className = 'test-result fail';
        }
      });
    });
  }

  function collectConfigFields(section, bid) {
    const data = state.backends[bid] || {};
    section.querySelectorAll('[data-field]').forEach(inp => {
      if (inp.value.trim()) data[inp.dataset.field] = inp.value.trim();
      else delete data[inp.dataset.field];
    });
    if (bid === 'openrouter' && !data.base_url) data.base_url = 'https://openrouter.ai/api/v1';
    state.backends[bid] = data;
  }

  async function loadOllamaModels() {
    const el = document.getElementById('setupOllamaModels');
    if (!el) return;
    const baseUrl = state.backends.ollama?.base_url || 'http://host.docker.internal:11434/v1';
    try {
      const res = await api('/api/setup/ollama/models?base_url=' + encodeURIComponent(baseUrl));
      if (res.models && res.models.length) {
        el.innerHTML = 'Models: ' + res.models.map(m => '<span>' + m + '</span>').join('');
      } else {
        el.textContent = 'No models found';
      }
    } catch { el.textContent = ''; }
  }

  // --- Step 1: Telegram ---
  let telegramRendered = false;
  function renderTelegram() {
    if (telegramRendered) return;
    telegramRendered = true;
    const el = modal.querySelector('[data-step="1"]');
    el.innerHTML =
      '<p style="font-size:0.82rem;color:var(--text-dim);margin:0 0 12px">Connect Telegram to chat with ALF from your phone. This step is optional.</p>' +
      '<div class="setup-tg-toggle"><input type="checkbox" id="setupTgEnable"><label for="setupTgEnable">Enable Telegram</label></div>' +
      '<div class="setup-tg-fields" id="setupTgFields" style="display:none">' +
        '<div class="form-group"><label>Bot Token</label><input type="text" class="input" id="setupTgToken" placeholder="123456789:ABCdef..."></div>' +
        '<div class="test-row"><button class="btn btn-sm" id="setupTgValidate">Validate</button><span class="setup-tg-result" id="setupTgResult"></span></div>' +
        '<div id="setupTgBotLink" style="display:none;margin:8px 0"></div>' +
        '<div class="form-group" style="margin-top:10px"><label>Chat ID</label>' +
          '<div style="display:flex;gap:8px;align-items:center">' +
            '<input type="text" class="input" id="setupTgChatId" placeholder="Your chat ID" style="flex:1">' +
            '<button class="btn btn-sm" id="setupTgGetChatId" style="white-space:nowrap">Get Chat ID</button>' +
          '</div>' +
        '</div>' +
        '<div id="setupTgChatIdResult" style="display:none;margin:-4px 0 8px"></div>' +
        '<small class="form-hint">Create a bot via <strong>@BotFather</strong>. After validating, open the bot link above, send a message, then click <em>Get Chat ID</em>.</small>' +
      '</div>';

    const toggle = document.getElementById('setupTgEnable');
    const fields = document.getElementById('setupTgFields');
    toggle.addEventListener('change', () => {
      fields.style.display = toggle.checked ? '' : 'none';
      if (!toggle.checked) state.telegram = null;
    });

    document.getElementById('setupTgValidate').addEventListener('click', async () => {
      const token = document.getElementById('setupTgToken').value.trim();
      const result = document.getElementById('setupTgResult');
      const botLinkEl = document.getElementById('setupTgBotLink');
      if (!token) { result.textContent = 'Enter a token'; result.className = 'setup-tg-result fail'; return; }
      result.textContent = 'Validating...';
      result.className = 'setup-tg-result';
      botLinkEl.style.display = 'none';
      try {
        const res = await api('/api/setup/telegram/validate', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ bot_token: token })
        });
        if (res.ok) {
          result.textContent = 'Bot: @' + res.bot_name;
          result.className = 'setup-tg-result ok';
          botLinkEl.innerHTML = '<a href="https://t.me/' + res.bot_name + '" target="_blank" style="color:var(--accent);font-size:0.85rem">Open @' + res.bot_name + ' in Telegram</a>' +
            '<span style="font-size:0.8rem;color:var(--text-dim);margin-left:8px">— send a message, then click Get Chat ID</span>';
          botLinkEl.style.display = '';
        } else {
          result.textContent = res.error || 'Invalid';
          result.className = 'setup-tg-result fail';
        }
      } catch (err) {
        result.textContent = err.error || 'Validation failed';
        result.className = 'setup-tg-result fail';
      }
    });

    document.getElementById('setupTgGetChatId').addEventListener('click', async () => {
      const token = document.getElementById('setupTgToken').value.trim();
      const chatIdInput = document.getElementById('setupTgChatId');
      const resultEl = document.getElementById('setupTgChatIdResult');
      if (!token) { resultEl.textContent = 'Validate the bot token first'; resultEl.className = 'setup-tg-result fail'; resultEl.style.display = ''; return; }
      resultEl.textContent = 'Checking for messages...';
      resultEl.className = 'setup-tg-result';
      resultEl.style.display = '';
      try {
        const res = await api('/api/setup/telegram/chatid', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ bot_token: token })
        });
        if (res.ok) {
          chatIdInput.value = res.chat_id;
          chatIdInput.dispatchEvent(new Event('input', { bubbles: true }));
          resultEl.textContent = 'Found: ' + (res.name || res.chat_id);
          resultEl.className = 'setup-tg-result ok';
        } else {
          resultEl.textContent = res.error || 'No messages found';
          resultEl.className = 'setup-tg-result fail';
        }
      } catch (err) {
        resultEl.textContent = err.error || 'Failed to retrieve chat ID';
        resultEl.className = 'setup-tg-result fail';
      }
    });

    // Collect on any input change
    el.querySelectorAll('input').forEach(inp => {
      inp.addEventListener('input', () => {
        if (toggle.checked) {
          const token = document.getElementById('setupTgToken').value.trim();
          const chatId = document.getElementById('setupTgChatId').value.trim();
          state.telegram = (token && chatId) ? { bot_token: token, chat_id: chatId } : null;
        }
      });
    });
  }

  // --- Step 2: Tiers ---
  let tiersRendered = false;
  function renderTiers() {
    if (tiersRendered) return;
    tiersRendered = true;
    const el = modal.querySelector('[data-step="2"]');
    el.innerHTML = '<p style="font-size:0.82rem;color:var(--text-dim);margin:0 0 12px">Choose a tier preset or keep your current configuration.</p>' +
      '<div id="setupPresetList"><p style="font-size:0.8rem;color:var(--text-dim)">Loading presets...</p></div>';

    api('/api/setup/presets').then(data => {
      const presets = data.presets || {};
      // Filter presets to only show those matching selected backends.
      const selectedBackends = Object.keys(state.backends);
      const all = [];
      Object.entries(presets).forEach(([backend, arr]) => {
        arr.forEach(p => {
          if (selectedBackends.includes(backend)) all.push(p);
        });
      });

      let html = '';
      if (all.length === 0) {
        html = '<div class="setup-preset-option selected" data-preset="">' +
          '<h4>Keep current tiers</h4><p>No presets available for your selected backends. Your current tier configuration will be preserved.</p></div>';
        state.presetId = '';
      } else {
        all.forEach((p, i) => {
          const sel = i === 0 ? ' selected' : '';
          html += '<div class="setup-preset-option' + sel + '" data-preset="' + p.id + '">' +
            '<h4>' + p.name + '</h4><p>' + p.description + '</p>';
          if (p.tiers && p.tiers.length) {
            html += '<div class="setup-preset-preview"><table><tr><th>Tier</th><th>Model</th><th>Priority</th></tr>';
            p.tiers.forEach(t => {
              html += '<tr><td>' + t.name + '</td><td>' + (t.model || '') + '</td><td>' + (t.priority || '') + '</td></tr>';
            });
            html += '</table></div>';
          }
          html += '</div>';
        });
        html += '<div class="setup-preset-option" data-preset="">' +
          '<h4>Keep current tiers</h4><p>Preserve your existing tier configuration.</p></div>';
        if (all.length > 0) state.presetId = all[0].id;
      }

      document.getElementById('setupPresetList').innerHTML = html;
      el.querySelectorAll('.setup-preset-option').forEach(opt => {
        opt.addEventListener('click', () => {
          el.querySelectorAll('.setup-preset-option').forEach(o => o.classList.remove('selected'));
          opt.classList.add('selected');
          state.presetId = opt.dataset.preset;
        });
      });
    }).catch(() => {
      document.getElementById('setupPresetList').innerHTML =
        '<div class="setup-preset-option selected" data-preset=""><h4>Keep current tiers</h4><p>Could not load presets.</p></div>';
    });
  }

  // --- Step 3: Done ---
  function renderDone() {
    const el = modal.querySelector('[data-step="3"]');
    const backendNames = Object.keys(state.backends);
    let html = '<p style="font-size:0.82rem;color:var(--text-dim);margin:0 0 12px">Review your configuration and apply.</p>';
    html += '<dl class="setup-recap">';
    html += '<dt>Backends</dt><dd>' + (backendNames.length ? backendNames.join(', ') : 'None selected') + '</dd>';
    html += '<dt>Telegram</dt><dd>' + (state.telegram ? 'Enabled' : 'Skipped') + '</dd>';
    html += '<dt>Tiers</dt><dd>' + (state.presetId ? 'Preset: ' + state.presetId : 'Keep current') + '</dd>';
    html += '</dl>';
    el.innerHTML = html;

    // Claude CLI: show auth status + terminal link (only if selected)
    if (backendNames.includes('claude')) {
      api('/api/setup/claude/check').then(r => {
        if (r.authenticated) {
          el.insertAdjacentHTML('beforeend',
            '<div class="setup-apply-info">' +
            '<strong>Claude</strong> - authenticated. ' +
            'Use the <a href="#" onclick="event.preventDefault();navigateTo(\'terminal\');" style="color:var(--accent)">Terminal</a> tab to interact with Claude directly.' +
            '</div>');
        } else {
          el.insertAdjacentHTML('beforeend',
            '<div class="setup-apply-warning">' +
            '<strong>Claude not authenticated</strong><br>' +
            'After setup, open the <a href="#" onclick="event.preventDefault();navigateTo(\'terminal\');" style="color:var(--accent)">Terminal</a> tab, type <code>claude</code>, then run <code>/login</code> to connect your Anthropic account. Type <code>/exit</code> when done.' +
            '</div>');
        }
      }).catch(() => {});
    }

    // Codex: show login hint if selected without API key
    if (backendNames.includes('codex')) {
      const codexKey = state.backends.codex && state.backends.codex.api_key;
      if (!codexKey) {
        el.insertAdjacentHTML('beforeend',
          '<div class="setup-apply-info">' +
          '<strong>OpenAI Codex</strong> — no API key provided. ' +
          'After setup, open the <a href="#" onclick="event.preventDefault();navigateTo(\'terminal\');" style="color:var(--accent)">Terminal</a> tab and run <code>codex login --device-auth</code> to authenticate with your ChatGPT subscription.' +
          '</div>');
      }
    }

    // Always show vault password — vault is needed for all secret storage.
    api('/api/vault/status').then(vs => {
      if (vs.status === 'locked' || vs.status === 'not_initialized') {
        const isNew = vs.status === 'not_initialized' || vs.first_time;
        let vaultHTML = '<div class="setup-vault-inline">' +
          '<label>Vault Password' + (isNew ? ' (new)' : '') + '</label>' +
          '<input type="password" class="input" id="setupVaultPw" placeholder="' +
            (isNew ? 'Choose a password (min. 8 characters)' : 'Enter your vault password') + '">';
        if (isNew) {
          vaultHTML += '<input type="password" class="input" id="setupVaultPwConfirm" placeholder="Confirm password" style="margin-top:6px">' +
            '<p class="form-hint" id="setupVaultPwMismatch" style="color:var(--danger);display:none">Passwords do not match</p>';
        }
        vaultHTML += '<p class="form-hint">' +
            (isNew ? 'This creates your encrypted vault for API keys, tokens, and secrets.' +
                     '<br><strong style="color:var(--danger)">Remember this password! If lost, the entire vault must be reset.</strong>' :
                     'Unlock your vault to store secrets.') + '</p></div>';
        el.insertAdjacentHTML('beforeend', vaultHTML);

        // Live validation for password confirmation
        if (isNew) {
          const pw = () => document.getElementById('setupVaultPw');
          const confirm = () => document.getElementById('setupVaultPwConfirm');
          const mismatch = () => document.getElementById('setupVaultPwMismatch');
          const check = () => {
            const c = confirm(), p = pw(), m = mismatch();
            if (c && p && m) {
              m.style.display = (c.value && c.value !== p.value) ? '' : 'none';
            }
          };
          setTimeout(() => {
            const c = confirm(), p = pw();
            if (c) c.addEventListener('input', check);
            if (p) p.addEventListener('input', check);
          }, 50);
        }
      }
    }).catch(() => {});
  }

  // --- Apply ---
  async function applySetup() {
    nextBtn.disabled = true;
    nextBtn.textContent = 'Applying...';
    const body = {};

    // Backends
    const backendNames = Object.keys(state.backends);
    if (backendNames.length) {
      body.backends = {};
      backendNames.forEach(bid => {
        if (bid === 'claude') return; // Claude uses CLI auth, not API key
        const b = state.backends[bid];
        body.backends[bid] = {};
        if (b.base_url) body.backends[bid].base_url = b.base_url;
        if (b.api_key) body.backends[bid].api_key = b.api_key;
        if (b.default_model) body.backends[bid].default_model = b.default_model;
        if (bid === 'ollama') body.backends[bid].auth = 'none';
      });
    }

    // Telegram
    if (state.telegram) body.telegram = state.telegram;

    // Preset
    if (state.presetId) body.preset_id = state.presetId;

    // Vault password
    const vpEl = document.getElementById('setupVaultPw');
    const vpConfirmEl = document.getElementById('setupVaultPwConfirm');
    if (vpEl && vpEl.value.trim()) {
      if (vpConfirmEl && vpConfirmEl.value.trim() !== vpEl.value.trim()) {
        nextBtn.disabled = false;
        nextBtn.textContent = 'Apply & Start';
        let errEl = modal.querySelector('.setup-apply-error');
        if (!errEl) {
          errEl = document.createElement('div');
          errEl.className = 'setup-apply-error';
          nextBtn.parentElement.parentElement.insertBefore(errEl, nextBtn.parentElement);
        }
        errEl.textContent = 'Vault passwords do not match';
        return;
      }
      body.vault_password = vpEl.value.trim();
    }

    try {
      const res = await api('/api/setup/apply', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      localStorage.setItem('alf-welcomed', '1');
      wb.classList.add('welcome-closing');
      setTimeout(() => wb.remove(), 300);
      toast('Setup complete');
      if (res.restart_required) toast('Restart required for Telegram', 'error');
      navigateTo('chat');
    } catch (err) {
      nextBtn.disabled = false;
      nextBtn.textContent = 'Apply & Start';
      const msg = err.error || 'Setup failed';
      // Show error inline in the wizard (toast is hidden behind backdrop).
      let errEl = modal.querySelector('.setup-apply-error');
      if (!errEl) {
        errEl = document.createElement('div');
        errEl.className = 'setup-apply-error';
        nextBtn.parentElement.parentElement.insertBefore(errEl, nextBtn.parentElement);
      }
      errEl.textContent = msg;
    }
  }

  goTo(0);
}

loadStatus();
loadTeachTiers();
loadApps();
mpCheckBadge();
wsInit();
setInterval(loadStatus, 30000);
setInterval(loadApps, 30000);
setInterval(mpCheckBadge, 60000);

// --- Firewall ---
let fwInitialized = false;
let fwCache = null;

function fwInit() {
  if (!fwInitialized) {
    fwInitialized = true;
    document.getElementById('fwRefreshBtn').addEventListener('click', fwLoad);
    document.getElementById('fwClearLogBtn').addEventListener('click', fwClearLog);
    document.getElementById('fwAddRuleBtn').addEventListener('click', () => fwShowRuleModal());

    // Mode toggle.
    document.querySelectorAll('#fwModeControl .seg-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('#fwModeControl .seg-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        fwSaveConfig();
      });
    });
  }
  fwLoad();
  fwStartAutoRefresh();
}

function fwStartAutoRefresh() {
  fwStopAutoRefresh();
  const cb = document.getElementById('fwAutoRefresh');
  if (cb && cb.checked) {
    fwAutoTimer = setInterval(fwLoad, 3000);
  }
}
function fwStopAutoRefresh() {
  if (fwAutoTimer) { clearInterval(fwAutoTimer); fwAutoTimer = null; }
}

document.addEventListener('change', e => {
  if (e.target.id === 'fwAutoRefresh') {
    if (e.target.checked) fwStartAutoRefresh(); else fwStopAutoRefresh();
  }
});

function fwLoad() {
  api('/api/firewall').then(data => {
    fwCache = data;
    fwRender();
  }).catch(() => {});
}

function fwRender() {
  if (!fwCache) return;
  const cfg = fwCache.config || {};
  const logEntries = fwCache.log || [];

  // Mode toggle.
  document.querySelectorAll('#fwModeControl .seg-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.value === cfg.mode);
  });

  // Rules list.
  const rulesList = document.getElementById('fwRulesList');
  const rules = cfg.rules || [];
  if (rules.length === 0) {
    rulesList.innerHTML = '<div class="fw-empty">No rules. All traffic is logged.</div>';
  } else {
    rulesList.innerHTML = rules.map((r, i) => `
      <div class="fw-rule-row">
        <code class="fw-rule-pattern">${esc(r.pattern)}</code>
        <span class="fw-rule-badge fw-rule-${r.action}">${r.action}</span>
        <button class="btn btn-icon btn-danger-icon fw-rule-del" data-idx="${i}" title="Delete rule"><i data-lucide="x"></i></button>
      </div>
    `).join('');
    lucide.createIcons();
    rulesList.querySelectorAll('.fw-rule-del').forEach(btn => {
      btn.addEventListener('click', () => {
        const idx = parseInt(btn.dataset.idx, 10);
        rules.splice(idx, 1);
        fwSaveConfig();
      });
    });
  }

  // Log table.
  const tbody = document.getElementById('fwLogBody');
  // Show newest first.
  const reversed = [...logEntries].reverse();
  tbody.innerHTML = reversed.map(e => {
    const t = new Date(e.time);
    const ts = t.toLocaleTimeString();
    const statusBadge = e.blocked
      ? '<span class="fw-status-badge fw-blocked">blocked</span>'
      : (e.status ? `<span class="fw-status-badge fw-allowed">${e.status}</span>` : '<span class="fw-status-badge fw-allowed">ok</span>');
    const hasDenyRule = (fwCache.config.rules || []).some(r => matchFwPattern(r.pattern, e.host) && r.action === 'deny');
    const denyBtn = (!e.blocked && !hasDenyRule)
      ? `<button class="btn btn-xs btn-deny-row" data-host="${esc(e.host)}" title="Add deny rule for ${esc(e.host)}">Deny</button>`
      : '';
    return `<tr class="${e.blocked ? 'fw-row-blocked' : ''}">
      <td>${ts}</td>
      <td>${esc(e.method)}</td>
      <td>${esc(e.host)}</td>
      <td class="fw-path-cell">${esc(e.path || '')}</td>
      <td>${statusBadge}</td>
      <td>${denyBtn}</td>
    </tr>`;
  }).join('');
}

function matchFwPattern(pattern, host) {
  pattern = pattern.toLowerCase();
  host = host.toLowerCase();
  if (pattern === '*') return true;
  if (pattern === host) return true;
  if (pattern.startsWith('*.')) {
    const suffix = pattern.slice(1); // ".example.com"
    return host.endsWith(suffix);
  }
  return false;
}

document.addEventListener('click', e => {
  const btn = e.target.closest('.btn-deny-row');
  if (!btn) return;
  const host = btn.dataset.host;
  if (!host || !fwCache) return;
  if (!fwCache.config.rules) fwCache.config.rules = [];
  fwCache.config.rules.push({ pattern: host, action: 'deny' });
  fwSaveConfig();
});

function fwSaveConfig() {
  if (!fwCache) return;
  const mode = document.querySelector('#fwModeControl .seg-btn.active')?.dataset.value || 'log-only';
  const cfg = { ...fwCache.config, mode, rules: fwCache.config.rules || [] };
  api('/api/firewall', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cfg),
  }).then(() => {
    showToast('Firewall config saved');
    fwLoad();
  }).catch(err => showToast('Save failed: ' + err.message, true));
}

function fwClearLog() {
  api('/api/firewall', { method: 'DELETE' }).then(() => {
    showToast('Log cleared');
    fwLoad();
  });
}

function fwShowRuleModal() {
  const old = document.getElementById('fwRuleModal');
  if (old) old.remove();
  const modal = document.createElement('div');
  modal.id = 'fwRuleModal';
  modal.className = 'modal-backdrop';
  modal.innerHTML = `
    <div class="modal">
      <h3>Add Rule</h3>
      <div class="form-row">
        <label>Pattern</label>
        <input type="text" id="fwRulePattern" placeholder="e.g. *.evil.com or example.com or *">
      </div>
      <div class="form-row">
        <label>Action</label>
        <select id="fwRuleAction">
          <option value="deny">Deny</option>
          <option value="allow">Allow</option>
        </select>
      </div>
      <div class="upload-actions">
        <button class="btn" id="fwRuleCancelBtn">Cancel</button>
        <button class="btn btn-primary" id="fwRuleSaveBtn">Add</button>
      </div>
    </div>
  `;
  document.body.appendChild(modal);
  document.getElementById('fwRuleCancelBtn').addEventListener('click', () => modal.remove());
  document.getElementById('fwRuleSaveBtn').addEventListener('click', () => {
    const pattern = document.getElementById('fwRulePattern').value.trim();
    const action = document.getElementById('fwRuleAction').value;
    if (!pattern) return;
    if (!fwCache.config.rules) fwCache.config.rules = [];
    fwCache.config.rules.push({ pattern, action });
    modal.remove();
    fwSaveConfig();
  });
}

function esc(s) {
  if (typeof esc._el === 'undefined') esc._el = document.createElement('span');
  esc._el.textContent = s;
  return esc._el.innerHTML;
}

// --- Terminal (xterm.js + WebSocket PTY) ---
var termThemes = {
  'Catppuccin Mocha': {
    background: '#1e1e2e', foreground: '#cdd6f4', cursor: '#f5e0dc', cursorAccent: '#1e1e2e',
    selectionBackground: 'rgba(108,123,196,0.4)', selectionForeground: '#ffffff',
    black: '#45475a', red: '#f38ba8', green: '#a6e3a1', yellow: '#f9e2af',
    blue: '#89b4fa', magenta: '#f5c2e7', cyan: '#94e2d5', white: '#bac2de',
    brightBlack: '#585b70', brightRed: '#f38ba8', brightGreen: '#a6e3a1', brightYellow: '#f9e2af',
    brightBlue: '#89b4fa', brightMagenta: '#f5c2e7', brightCyan: '#94e2d5', brightWhite: '#a6adc8',
  },
  'Catppuccin Latte': {
    background: '#eff1f5', foreground: '#4c4f69', cursor: '#dc8a78', cursorAccent: '#eff1f5',
    selectionBackground: 'rgba(0,90,200,0.25)', selectionForeground: '#000000',
    black: '#5c5f77', red: '#d20f39', green: '#40a02b', yellow: '#df8e1d',
    blue: '#1e66f5', magenta: '#ea76cb', cyan: '#179299', white: '#acb0be',
    brightBlack: '#6c6f85', brightRed: '#d20f39', brightGreen: '#40a02b', brightYellow: '#df8e1d',
    brightBlue: '#1e66f5', brightMagenta: '#ea76cb', brightCyan: '#179299', brightWhite: '#bcc0cc',
  },
  'Dracula': {
    background: '#282a36', foreground: '#f8f8f2', cursor: '#f8f8f2', cursorAccent: '#282a36',
    selectionBackground: 'rgba(68,71,90,0.6)', selectionForeground: '#f8f8f2',
    black: '#21222c', red: '#ff5555', green: '#50fa7b', yellow: '#f1fa8c',
    blue: '#bd93f9', magenta: '#ff79c6', cyan: '#8be9fd', white: '#f8f8f2',
    brightBlack: '#6272a4', brightRed: '#ff6e6e', brightGreen: '#69ff94', brightYellow: '#ffffa5',
    brightBlue: '#d6acff', brightMagenta: '#ff92df', brightCyan: '#a4ffff', brightWhite: '#ffffff',
  },
  'Solarized Dark': {
    background: '#002b36', foreground: '#839496', cursor: '#93a1a1', cursorAccent: '#002b36',
    selectionBackground: 'rgba(147,161,161,0.3)', selectionForeground: '#fdf6e3',
    black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
    blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
    brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#586e75', brightYellow: '#657b83',
    brightBlue: '#839496', brightMagenta: '#6c71c4', brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
  },
  'Solarized Light': {
    background: '#fdf6e3', foreground: '#657b83', cursor: '#586e75', cursorAccent: '#fdf6e3',
    selectionBackground: 'rgba(0,90,200,0.2)', selectionForeground: '#002b36',
    black: '#073642', red: '#dc322f', green: '#859900', yellow: '#b58900',
    blue: '#268bd2', magenta: '#d33682', cyan: '#2aa198', white: '#eee8d5',
    brightBlack: '#586e75', brightRed: '#cb4b16', brightGreen: '#586e75', brightYellow: '#657b83',
    brightBlue: '#839496', brightMagenta: '#6c71c4', brightCyan: '#93a1a1', brightWhite: '#fdf6e3',
  },
  'Tokyo Night': {
    background: '#1a1b26', foreground: '#a9b1d6', cursor: '#c0caf5', cursorAccent: '#1a1b26',
    selectionBackground: 'rgba(40,52,100,0.6)', selectionForeground: '#c0caf5',
    black: '#15161e', red: '#f7768e', green: '#9ece6a', yellow: '#e0af68',
    blue: '#7aa2f7', magenta: '#bb9af7', cyan: '#7dcfff', white: '#a9b1d6',
    brightBlack: '#414868', brightRed: '#f7768e', brightGreen: '#9ece6a', brightYellow: '#e0af68',
    brightBlue: '#7aa2f7', brightMagenta: '#bb9af7', brightCyan: '#7dcfff', brightWhite: '#c0caf5',
  },
  'GitHub Dark': {
    background: '#0d1117', foreground: '#c9d1d9', cursor: '#c9d1d9', cursorAccent: '#0d1117',
    selectionBackground: 'rgba(56,139,253,0.3)', selectionForeground: '#f0f6fc',
    black: '#484f58', red: '#ff7b72', green: '#3fb950', yellow: '#d29922',
    blue: '#58a6ff', magenta: '#bc8cff', cyan: '#39c5cf', white: '#b1bac4',
    brightBlack: '#6e7681', brightRed: '#ffa198', brightGreen: '#56d364', brightYellow: '#e3b341',
    brightBlue: '#79c0ff', brightMagenta: '#d2a8ff', brightCyan: '#56d4dd', brightWhite: '#f0f6fc',
  },
  'Nord': {
    background: '#2e3440', foreground: '#d8dee9', cursor: '#d8dee9', cursorAccent: '#2e3440',
    selectionBackground: 'rgba(136,192,208,0.3)', selectionForeground: '#eceff4',
    black: '#3b4252', red: '#bf616a', green: '#a3be8c', yellow: '#ebcb8b',
    blue: '#81a1c1', magenta: '#b48ead', cyan: '#88c0d0', white: '#e5e9f0',
    brightBlack: '#4c566a', brightRed: '#bf616a', brightGreen: '#a3be8c', brightYellow: '#ebcb8b',
    brightBlue: '#81a1c1', brightMagenta: '#b48ead', brightCyan: '#8fbcbb', brightWhite: '#eceff4',
  },
  'Sage Light': {
    background: '#f0f3ec', foreground: '#3a4a3a', cursor: '#5a8f5a', cursorAccent: '#f0f3ec',
    selectionBackground: 'rgba(90,143,90,0.25)', selectionForeground: '#1a2a1a',
    black: '#3a4a3a', red: '#c4392a', green: '#3d8b3d', yellow: '#b8860b',
    blue: '#3a8da8', magenta: '#7a5cad', cyan: '#2d8a7a', white: '#bcc5b8',
    brightBlack: '#6b7b6b', brightRed: '#c4392a', brightGreen: '#3d8b3d', brightYellow: '#b8860b',
    brightBlue: '#3a8da8', brightMagenta: '#7a5cad', brightCyan: '#2d8a7a', brightWhite: '#d8ddd3',
  },
  'Sage Dark': {
    background: '#222822', foreground: '#c5cfbf', cursor: '#7cb87c', cursorAccent: '#222822',
    selectionBackground: 'rgba(124,184,124,0.3)', selectionForeground: '#e0e8da',
    black: '#2e342e', red: '#e07060', green: '#8ec48e', yellow: '#d4a84b',
    blue: '#6ab4cc', magenta: '#b89adb', cyan: '#6ec4b0', white: '#9aa894',
    brightBlack: '#3a4638', brightRed: '#e07060', brightGreen: '#8ec48e', brightYellow: '#d4a84b',
    brightBlue: '#6ab4cc', brightMagenta: '#b89adb', brightCyan: '#6ec4b0', brightWhite: '#c5cfbf',
  },
  'Studio Light': {
    background: '#f5f3f0', foreground: '#2c2a28', cursor: '#d97706', cursorAccent: '#f5f3f0',
    selectionBackground: 'rgba(217,119,6,0.2)', selectionForeground: '#2c2a28',
    black: '#2c2a28', red: '#dc2626', green: '#16a34a', yellow: '#ca8a04',
    blue: '#3a8da8', magenta: '#7a5cad', cyan: '#2d8a7a', white: '#d6d3cf',
    brightBlack: '#8a8580', brightRed: '#dc2626', brightGreen: '#16a34a', brightYellow: '#ca8a04',
    brightBlue: '#3a8da8', brightMagenta: '#c76fa0', brightCyan: '#2d8a7a', brightWhite: '#f5f3f0',
  },
  'Studio Dark': {
    background: '#1c1c1c', foreground: '#e7e5e4', cursor: '#f59e0b', cursorAccent: '#1c1c1c',
    selectionBackground: 'rgba(245,158,11,0.25)', selectionForeground: '#e7e5e4',
    black: '#262626', red: '#ef4444', green: '#22c55e', yellow: '#eab308',
    blue: '#6ab4cc', magenta: '#b89adb', cyan: '#6ec4b0', white: '#a8a29e',
    brightBlack: '#333333', brightRed: '#ef4444', brightGreen: '#22c55e', brightYellow: '#eab308',
    brightBlue: '#6ab4cc', brightMagenta: '#dea0c4', brightCyan: '#6ec4b0', brightWhite: '#e7e5e4',
  },
};

var termInstance = null;
var termWS = null;
var termFitAddon = null;
var termResizeObserver = null;

// Populate theme selector.
(function() {
  const sel = document.getElementById('termThemeSelect');
  Object.keys(termThemes).forEach(name => {
    const opt = document.createElement('option');
    opt.value = name;
    opt.textContent = name;
    sel.appendChild(opt);
  });
  const saved = localStorage.getItem('alf-term-theme');
  if (saved && termThemes[saved]) sel.value = saved;
  else {
    const palette = localStorage.getItem('alf-palette') ?? 'sage';
    const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    const t = ALF_THEMES[palette];
    if (t) sel.value = dark ? t.termDark : t.termLight;
  }

  sel.addEventListener('change', () => {
    localStorage.setItem('alf-term-theme', sel.value);
    if (termInstance) {
      termInstance.options.theme = termThemes[sel.value];
    }
  });
})();

function termGetTheme() {
  const sel = document.getElementById('termThemeSelect');
  if (termThemes[sel.value]) return termThemes[sel.value];
  const palette = localStorage.getItem('alf-palette') ?? 'sage';
  const dark = window.matchMedia('(prefers-color-scheme: dark)').matches;
  const t = ALF_THEMES[palette];
  if (t) {
    const name = dark ? t.termDark : t.termLight;
    if (termThemes[name]) return termThemes[name];
  }
  return termThemes[dark ? 'Catppuccin Mocha' : 'Catppuccin Latte'];
}

function terminalInit() {
  // termThemes is declared later in the file; if not yet initialized, defer.
  if (!termThemes) { setTimeout(terminalInit, 0); return; }
  if (termInstance && termWS && termWS.readyState === WebSocket.OPEN) {
    termFitAddon.fit();
    termInstance.focus();
    return;
  }
  terminalStart();
}

function terminalStart() {
  // Cleanup previous session.
  if (termWS) { termWS.close(); termWS = null; }
  if (termInstance) { termInstance.dispose(); termInstance = null; }
  if (termResizeObserver) { termResizeObserver.disconnect(); termResizeObserver = null; }

  const container = document.getElementById('terminalContainer');
  container.innerHTML = '';

  const term = new Terminal({
    cursorBlink: true,
    cursorStyle: 'block',
    fontSize: 14,
    fontFamily: 'Menlo, Monaco, Consolas, monospace',
    theme: termGetTheme(),
    scrollback: 5000,
    fastScrollModifier: 'alt',
    allowProposedApi: true,
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  const webLinksAddon = new WebLinksAddon.WebLinksAddon();
  term.loadAddon(webLinksAddon);
  term.open(container);
  termInstance = term;
  termFitAddon = fitAddon;

  // Wait for layout to settle, then fit + connect.
  setTimeout(() => {
    fitAddon.fit();

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(proto + '//' + location.host + '/api/terminal');
    ws.binaryType = 'arraybuffer';
    termWS = ws;

    function sendSize() {
      if (ws.readyState !== WebSocket.OPEN) return;
      const buf = new Uint8Array(5);
      buf[0] = 1;
      buf[1] = (term.cols >> 8) & 0xff;
      buf[2] = term.cols & 0xff;
      buf[3] = (term.rows >> 8) & 0xff;
      buf[4] = term.rows & 0xff;
      ws.send(buf);
    }

    ws.onopen = () => sendSize();

    // Buffer terminal output to detect long URLs (e.g. OAuth) that wrap
    // across multiple lines and are hard to click/copy in a terminal.
    let termUrlBuf = '';
    let termUrlTimer = null;
    function termCheckForUrl() {
      // Match URLs ≥120 chars (OAuth links are typically 200+).
      const m = termUrlBuf.match(/https?:\/\/[^\s\x1b]{120,}/);
      if (m) {
        const url = m[0];
        termShowUrlBar(url);
      }
      termUrlBuf = '';
    }

    ws.onmessage = (ev) => {
      const bytes = new Uint8Array(ev.data);
      term.write(bytes);
      // Accumulate text for URL detection (strip most control chars).
      const chunk = new TextDecoder().decode(bytes).replace(/\x1b\[[0-9;]*[a-zA-Z]/g, '').replace(/[\r\n]/g, '');
      termUrlBuf += chunk;
      clearTimeout(termUrlTimer);
      termUrlTimer = setTimeout(termCheckForUrl, 300);
    };

    ws.onclose = () => {
      term.write('\r\n\x1b[90m[session ended -click New Session to reconnect]\x1b[0m\r\n');
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(data);
      }
    });

    let resizeTimeout = null;
    let lastTermWidth = container.clientWidth;
    let lastTermHeight = container.clientHeight;
    function onResize() {
      clearTimeout(resizeTimeout);
      resizeTimeout = setTimeout(() => {
        // Only re-fit if the container actually changed size.
        // Avoids scroll jumps from spurious ResizeObserver events.
        const w = container.clientWidth;
        const h = container.clientHeight;
        if (w === lastTermWidth && h === lastTermHeight) return;
        lastTermWidth = w;
        lastTermHeight = h;
        fitAddon.fit();
        sendSize();
      }, 150);
    }

    window.addEventListener('resize', onResize);
    termResizeObserver = new ResizeObserver(onResize);
    termResizeObserver.observe(container);

    term.focus();
  }, 50);
}

document.getElementById('termNewBtn').addEventListener('click', terminalStart);

// URL action bar -shown when a long URL is detected in terminal output.
function termShowUrlBar(url) {
  let bar = document.getElementById('termUrlBar');
  if (bar) bar.remove();
  bar = document.createElement('div');
  bar.id = 'termUrlBar';
  bar.innerHTML =
    '<span class="term-url-label">Link detected</span>' +
    '<button class="btn-sm" id="termUrlOpen">Open</button>' +
    '<button class="btn-sm" id="termUrlCopy">Copy URL</button>' +
    '<button class="btn-sm btn-ghost" id="termUrlDismiss">&times;</button>';
  document.getElementById('terminalContainer').prepend(bar);
  document.getElementById('termUrlOpen').onclick = () => { window.open(url, '_blank'); };
  document.getElementById('termUrlCopy').onclick = async () => {
    try { await navigator.clipboard.writeText(url); toast('URL copied'); } catch {}
  };
  document.getElementById('termUrlDismiss').onclick = () => bar.remove();
  // Auto-dismiss after 60s.
  setTimeout(() => { if (bar.parentNode) bar.remove(); }, 60000);
}

// --- Terminal input support ---
const isTouchDevice = ('ontouchstart' in window) || navigator.maxTouchPoints > 0;

// Paste button -read clipboard and send to terminal.
// Uses Clipboard API with fallback for mobile browsers that deny readText().
document.getElementById('termPasteBtn').addEventListener('click', async () => {
  if (!termWS || termWS.readyState !== WebSocket.OPEN) return;
  // Try Clipboard API first (works on desktop + Android Chrome + iOS 16+).
  try {
    const text = await navigator.clipboard.readText();
    if (text) {
      termWS.send(text);
      if (termInstance) termInstance.focus();
      return;
    }
  } catch { /* permission denied or not supported */ }
  // Fallback: focus the input bar so the user can long-press > paste natively.
  const inp = document.getElementById('termInput');
  inp.value = '';
  inp.focus();
  toast('Paste into the input field below', 'info');
});

// Copy button -copy xterm selection to clipboard.
document.getElementById('termCopyBtn').addEventListener('click', async () => {
  if (!termInstance) return;
  const sel = termInstance.getSelection();
  if (sel) {
    try { await navigator.clipboard.writeText(sel); } catch {}
  }
});

// Mobile input bar -type/paste text and send with Enter or button.
const termInput = document.getElementById('termInput');
const termSendBtn = document.getElementById('termSendBtn');

function termSendInput() {
  if (!termWS || termWS.readyState !== WebSocket.OPEN) return;
  const text = termInput.value;
  if (!text) return;
  termWS.send(text + '\n');
  termInput.value = '';
}

termSendBtn.addEventListener('click', termSendInput);
// Auto-send pasted content (no need to press Enter after pasting).
termInput.addEventListener('paste', (e) => {
  setTimeout(() => {
    if (termInput.value) termSendInput();
  }, 0);
});
termInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') { e.preventDefault(); termSendInput(); }
  // Send Ctrl+C
  if (e.key === 'c' && e.ctrlKey) {
    e.preventDefault();
    if (termWS && termWS.readyState === WebSocket.OPEN) termWS.send('\x03');
    termInput.value = '';
  }
});

// --- Terminal long-press context menu ---
let termLongPressTimer = null;
const LONG_PRESS_MS = 500;

function termShowContextMenu(x, y) {
  termHideContextMenu();
  const menu = document.createElement('div');
  menu.className = 'term-context-menu';
  menu.id = 'termContextMenu';

  const hasSel = termInstance && termInstance.getSelection();

  const items = [
    ...(hasSel ? [{ label: 'Copy', icon: 'copy', action: 'copy' }] : []),
    { label: 'Paste', icon: 'clipboard-paste', action: 'paste' },
    { label: 'Select All', icon: 'text-select', action: 'selectall' },
  ];

  items.forEach(item => {
    const btn = document.createElement('button');
    btn.innerHTML = '<i data-lucide="' + item.icon + '"></i> ' + item.label;
    btn.addEventListener('click', async (e) => {
      e.stopPropagation();
      if (item.action === 'copy') {
        const sel = termInstance.getSelection();
        if (sel) try { await navigator.clipboard.writeText(sel); } catch {}
      } else if (item.action === 'paste') {
        if (termWS && termWS.readyState === WebSocket.OPEN) {
          try {
            const text = await navigator.clipboard.readText();
            if (text) { termWS.send(text); return; }
          } catch {}
          // Fallback: focus input bar for native paste.
          const inp = document.getElementById('termInput');
          inp.value = '';
          inp.focus();
          toast('Paste into the input field below', 'info');
        }
      } else if (item.action === 'selectall') {
        if (termInstance) termInstance.selectAll();
      }
      termHideContextMenu();
    });
    menu.appendChild(btn);
  });

  // Position within viewport bounds.
  document.body.appendChild(menu);
  const rect = menu.getBoundingClientRect();
  const maxX = window.innerWidth - rect.width - 8;
  const maxY = window.innerHeight - rect.height - 8;
  menu.style.left = Math.min(x, maxX) + 'px';
  menu.style.top = Math.min(y, maxY) + 'px';

  lucide.createIcons({ nodes: menu.querySelectorAll('[data-lucide]') });

  // Dismiss on any tap outside.
  setTimeout(() => document.addEventListener('touchstart', termHideContextMenu, { once: true }), 10);
}

function termHideContextMenu() {
  const m = document.getElementById('termContextMenu');
  if (m) m.remove();
}

if (isTouchDevice) {
  const container = document.getElementById('terminalContainer');
  container.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1) return;
    const touch = e.touches[0];
    termLongPressTimer = setTimeout(() => {
      termShowContextMenu(touch.clientX, touch.clientY);
    }, LONG_PRESS_MS);
  }, { passive: true });

  container.addEventListener('touchmove', () => {
    clearTimeout(termLongPressTimer);
  }, { passive: true });

  container.addEventListener('touchend', () => {
    clearTimeout(termLongPressTimer);
  }, { passive: true });
}

// ========== Vault ==========

var vaultInited = false;
var vaultSecretsCache = []; // cached secret names for secret pickers

function vaultInit() {
  if (vaultInited) { vaultRefresh(); return; }
  vaultInited = true;

  document.getElementById('vaultUnlockBtn').addEventListener('click', vaultUnlock);
  document.getElementById('vaultPasswordInput').addEventListener('keydown', e => {
    if (e.key === 'Enter') vaultUnlock();
  });
  document.getElementById('vaultSetupBtn').addEventListener('click', vaultSetup);
  document.getElementById('vaultSetupPassword').addEventListener('keydown', e => {
    if (e.key === 'Enter') vaultSetup();
  });
  document.getElementById('vaultResetBtn').addEventListener('click', vaultReset);
  document.getElementById('vaultLockBtn').addEventListener('click', vaultLock);
  document.getElementById('vaultRefreshBtn').addEventListener('click', vaultRefresh);
  document.getElementById('vaultAddServiceBtn').addEventListener('click', () => vaultShowServiceModal());
  document.getElementById('vaultSvcCancelBtn').addEventListener('click', () => {
    document.getElementById('vaultServiceModal').style.display = 'none';
  });
  document.getElementById('vaultSvcSaveBtn').addEventListener('click', vaultSaveService);
  document.getElementById('vaultSvcAuthType').addEventListener('change', vaultToggleAuthFields);
  document.getElementById('vaultCreateTokenBtn').addEventListener('click', () => vaultCreateTokenModal());
  document.getElementById('vaultFileInput').addEventListener('change', vaultUploadFileModal);
  document.getElementById('vaultExportBtn').addEventListener('click', vaultExportModal);
  document.getElementById('vaultImportBtn').addEventListener('click', () => document.getElementById('vaultImportFile').click());
  document.getElementById('vaultImportFile').addEventListener('change', vaultImportModal);
  document.getElementById('vaultAddSecretBtn').addEventListener('click', () => vaultAddSecretModal());
  document.getElementById('vaultOAuth2TabBrowser').addEventListener('click', () => vaultOAuth2SetMode('browser'));
  document.getElementById('vaultOAuth2TabManual').addEventListener('click', () => vaultOAuth2SetMode('manual'));
  document.getElementById('vaultOAuth2AuthorizeBtn').addEventListener('click', vaultOAuth2StartFlow);

  // Generic modal cancel/close
  document.getElementById('vaultGenericModalCancel').addEventListener('click', vaultModalClose);

  vaultRefresh();
}

// ========== Generic Vault Modal ==========

function vaultModal(opts) {
  // opts: { title, fields: [{id, label, type, placeholder, options?, value?, disabled?, hint?}], saveLabel?, onSave }
  return new Promise((resolve) => {
    const overlay = document.getElementById('vaultGenericModal');
    const titleEl = document.getElementById('vaultGenericModalTitle');
    const body = document.getElementById('vaultGenericModalBody');
    const saveBtn = document.getElementById('vaultGenericModalSave');

    titleEl.textContent = opts.title || 'Action';
    saveBtn.textContent = opts.saveLabel || 'Save';
    body.innerHTML = '';

    for (const f of (opts.fields || [])) {
      const group = document.createElement('div');
      group.className = 'form-group';
      group.style.marginBottom = '12px';

      if (f.label) {
        const label = document.createElement('label');
        label.textContent = f.label;
        group.appendChild(label);
      }

      if (f.type === 'select') {
        const sel = document.createElement('select');
        sel.className = 'input';
        sel.id = 'vm_' + f.id;
        for (const o of (f.options || [])) {
          const opt = document.createElement('option');
          opt.value = typeof o === 'object' ? o.value : o;
          opt.textContent = typeof o === 'object' ? o.label : o;
          sel.appendChild(opt);
        }
        if (f.value) sel.value = f.value;
        if (f.disabled) sel.disabled = true;
        if (f.onChange) sel.addEventListener('change', () => f.onChange(sel.value));
        group.appendChild(sel);
      } else if (f.type === 'file') {
        const inp = document.createElement('input');
        inp.type = 'file';
        inp.className = 'input';
        inp.id = 'vm_' + f.id;
        if (f.accept) inp.accept = f.accept;
        group.appendChild(inp);
      } else if (f.type === 'info') {
        const p = document.createElement('p');
        p.style.cssText = 'font-size:0.85rem;color:var(--text-dim);margin:4px 0';
        p.textContent = f.value || '';
        p.id = 'vm_' + f.id;
        group.appendChild(p);
      } else {
        const inp = document.createElement('input');
        inp.type = f.type || 'text';
        inp.className = 'input';
        inp.id = 'vm_' + f.id;
        inp.placeholder = f.placeholder || '';
        if (f.value) inp.value = f.value;
        if (f.disabled) inp.readOnly = true;
        group.appendChild(inp);
      }

      if (f.hint) {
        const hint = document.createElement('span');
        hint.className = 'form-hint';
        hint.textContent = f.hint;
        group.appendChild(hint);
      }

      body.appendChild(group);
    }

    function cleanup() {
      overlay.style.display = 'none';
      saveBtn.removeEventListener('click', onSave);
    }

    function onSave() {
      const values = {};
      for (const f of (opts.fields || [])) {
        const el = document.getElementById('vm_' + f.id);
        if (!el) continue;
        if (f.type === 'file') {
          values[f.id] = el.files[0] || null;
        } else {
          values[f.id] = el.value;
        }
      }
      cleanup();
      resolve(values);
    }

    // Override cancel to reject
    const cancelBtn = document.getElementById('vaultGenericModalCancel');
    const origCancel = cancelBtn.onclick;
    cancelBtn.onclick = () => { cleanup(); resolve(null); };

    saveBtn.addEventListener('click', onSave);
    overlay.style.display = '';

    // Focus first input
    const firstInput = body.querySelector('input:not([type="file"]), select');
    if (firstInput) setTimeout(() => firstInput.focus(), 50);
  });
}

function vaultModalClose() {
  document.getElementById('vaultGenericModal').style.display = 'none';
}

async function vaultRefresh() {
  try {
    const data = await api('/api/vault/status');
    const dot = document.getElementById('vaultStatusDot');
    const text = document.getElementById('vaultStatusText');
    const setupCard = document.getElementById('vaultSetupCard');
    const unlockCard = document.getElementById('vaultUnlockCard');
    const lockBtn = document.getElementById('vaultLockBtn');
    const servicesCard = document.getElementById('vaultServicesCard');
    const filesCard = document.getElementById('vaultFilesCard');
    const tokensCard = document.getElementById('vaultTokensCard');
    const secretsCard = document.getElementById('vaultSecretsCard');
    const exportBtn = document.getElementById('vaultExportBtn');
    const importBtn = document.getElementById('vaultImportBtn');

    // Hide everything first.
    setupCard.style.display = 'none';
    unlockCard.style.display = 'none';
    lockBtn.style.display = 'none';
    exportBtn.style.display = 'none';
    importBtn.style.display = 'none';
    servicesCard.style.display = 'none';
    filesCard.style.display = 'none';
    tokensCard.style.display = 'none';
    secretsCard.style.display = 'none';

    if (!data || !data.available) {
      dot.className = 'vault-status-indicator vault-status-off';
      text.textContent = 'Vault not available (binary missing)';
      return;
    }

    if (data.status === 'unlocked') {
      dot.className = 'vault-status-indicator vault-status-on';
      text.textContent = 'Unlocked';
      lockBtn.style.display = '';
      exportBtn.style.display = '';
      importBtn.style.display = '';
      servicesCard.style.display = '';
      secretsCard.style.display = '';
      filesCard.style.display = '';
      tokensCard.style.display = '';
      vaultLoadServices();
      vaultLoadSecrets();
      vaultLoadFiles();
      vaultLoadTokens();
    } else if (data.first_time) {
      dot.className = 'vault-status-indicator vault-status-off';
      text.textContent = 'Not configured';
      setupCard.style.display = '';
    } else {
      dot.className = 'vault-status-indicator vault-status-locked';
      text.textContent = data.status === 'unreachable' ? 'Unreachable' : 'Locked';
      unlockCard.style.display = '';
    }
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    const dot = document.getElementById('vaultStatusDot');
    dot.className = 'vault-status-indicator vault-status-off';
    document.getElementById('vaultStatusText').textContent = msg === 'vault not available'
      ? 'Not available (vault-server not found)'
      : 'Error: ' + msg;
  }
}

async function vaultSetup() {
  const pw = document.getElementById('vaultSetupPassword').value.trim();
  if (!pw) return;
  if (pw.length < 8) { alert('Password must be at least 8 characters.'); return; }
  try {
    await api('/api/vault/unlock', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pw })
    });
    document.getElementById('vaultSetupPassword').value = '';
    vaultRefresh();
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    alert('Setup failed: ' + msg);
  }
}

async function vaultReset() {
  if (!confirm('Reset the vault?\n\n⚠ This will permanently delete ALL stored credentials, services, and tokens.\nYou will need to re-configure all API services from scratch.\nAlf will lose access to all external APIs.\n\nThis cannot be undone. Continue?')) return;
  try {
    await api('/api/vault/reset', { method: 'POST' });
    vaultRefresh();
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    alert('Reset failed: ' + msg);
  }
}

async function vaultUnlock() {
  const pw = document.getElementById('vaultPasswordInput').value.trim();
  if (!pw) return;
  try {
    await api('/api/vault/unlock', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pw })
    });
    document.getElementById('vaultPasswordInput').value = '';
    vaultRefresh();
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    alert('Unlock failed: ' + msg);
  }
}

async function vaultLock() {
  const ok = confirm(
    'Lock the vault?\n\n' +
    '⚠ This will immediately revoke all tokens and disable API proxy access.\n' +
    'Alf will no longer be able to call external APIs (vault proxy) until you unlock again.\n' +
    'Scheduled jobs that use vault services will fail.\n\n' +
    'Continue?'
  );
  if (!ok) return;
  try {
    await api('/api/vault/lock', { method: 'POST' });
    vaultRefresh();
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    alert('Lock failed: ' + msg);
  }
}

let vaultServicesCache = [];

async function vaultLoadServices() {
  try {
    const services = await api('/api/vault/services');
    const list = document.getElementById('vaultServicesList');
    if (!services || services.length === 0) {
      list.innerHTML = '<div class="vault-empty">Add a service to store its API credentials, then use Access Keys to let apps call it through the proxy.</div>';
      return;
    }
    vaultServicesCache = services;
    // Load ref mappings for display
    let serviceRefs = {};
    try { serviceRefs = JSON.parse(localStorage.getItem('alf_vault_service_refs') || '{}'); } catch (_) {}
    list.innerHTML = services.map(s => {
      const refs = serviceRefs[s.name] || {};
      const refLabels = Object.entries(refs).filter(([,v]) => v).map(([k,v]) => esc(v));
      const refBadge = refLabels.length > 0
        ? ' <span class="vault-ref-badge">via ' + refLabels.join(', ') + '</span>'
        : '';
      const tokenBadge = s.token_status === 'expired'
        ? ' <span class="vault-token-badge vault-token-expired">expired</span>'
        : s.token_status === 'expiring'
        ? ' <span class="vault-token-badge vault-token-expiring">expiring</span>'
        : s.token_status === 'valid'
        ? ' <span class="vault-token-badge vault-token-valid">valid</span>'
        : '';
      return `
      <div class="vault-item">
        <div class="vault-item-info">
          <span class="vault-item-name">${esc(s.name)}${refBadge}${tokenBadge}</span>
          <span class="vault-item-detail">${esc(s.base_url)} &middot; ${esc(s.auth_type)}${s.tls_skip_verify ? ' &middot; TLS skip' : ''}${s.expires_at ? ' &middot; expires ' + new Date(s.expires_at * 1000).toLocaleTimeString() : ''}</span>
        </div>
        <div class="vault-item-actions">
          <button class="btn btn-icon vault-edit-btn" data-name="${esc(s.name)}" title="Edit"><i data-lucide="pencil"></i></button>
          <button class="btn btn-icon vault-test-btn" data-name="${esc(s.name)}" title="Test"><i data-lucide="zap"></i></button>
          <button class="btn btn-icon vault-del-btn" data-name="${esc(s.name)}" title="Delete"><i data-lucide="trash-2"></i></button>
        </div>
      </div>`;
    }).join('');
    lucide.createIcons();
    list.querySelectorAll('.vault-edit-btn').forEach(btn => {
      btn.addEventListener('click', () => vaultEditService(btn.dataset.name));
    });
    list.querySelectorAll('.vault-test-btn').forEach(btn => {
      btn.addEventListener('click', () => vaultTestService(btn.dataset.name, btn));
    });
    list.querySelectorAll('.vault-del-btn').forEach(btn => {
      btn.addEventListener('click', () => vaultDeleteService(btn.dataset.name));
    });
  } catch (err) {
    document.getElementById('vaultServicesList').innerHTML = '<div class="vault-empty">Error loading services</div>';
  }
}

async function vaultTestService(name, btn) {
  btn.disabled = true;
  try {
    const result = await api('/api/vault/services/' + encodeURIComponent(name) + '/test', { method: 'POST' });
    btn.classList.add(result.ok ? 'vault-test-ok' : 'vault-test-fail');
    setTimeout(() => btn.classList.remove('vault-test-ok', 'vault-test-fail'), 2000);
  } catch (err) {
    btn.classList.add('vault-test-fail');
    setTimeout(() => btn.classList.remove('vault-test-fail'), 2000);
  }
  btn.disabled = false;
}

async function vaultDeleteService(name) {
  if (!confirm('Delete service "' + name + '"?')) return;
  try {
    await api('/api/vault/services/' + encodeURIComponent(name), { method: 'DELETE' });
    vaultLoadServices();
  } catch (err) {
    alert('Delete failed: ' + err.message);
  }
}

function vaultShowServiceModal(edit) {
  document.getElementById('vaultServiceModalTitle').textContent = edit ? 'Edit Service' : 'Add Service';
  document.getElementById('vaultSvcName').value = edit ? edit.name : '';
  document.getElementById('vaultSvcName').readOnly = !!edit;
  document.getElementById('vaultSvcBaseURL').value = edit ? edit.base_url : '';
  document.getElementById('vaultSvcAuthType').value = edit ? (edit.auth_type || 'bearer') : 'bearer';
  document.getElementById('vaultSvcToken').value = '';
  document.getElementById('vaultSvcHeaderName').value = '';
  document.getElementById('vaultSvcHeaderValue').value = '';
  document.getElementById('vaultSvcUsername').value = '';
  document.getElementById('vaultSvcPassword').value = '';
  document.getElementById('vaultSvcOAuthClientId').value = '';
  document.getElementById('vaultSvcOAuthClientSecret').value = '';
  document.getElementById('vaultSvcOAuthTokenUrl').value = '';
  document.getElementById('vaultSvcOAuthRefreshToken').value = '';
  document.getElementById('vaultSvcOAuthScopes').value = edit && edit.scopes ? edit.scopes.join(', ') : '';
  document.getElementById('vaultSvcSAFileRef').value = '';
  document.getElementById('vaultSvcSAScopes').value = edit && edit.scopes ? edit.scopes.join(', ') : '';
  document.getElementById('vaultSvcSATokenUrl').value = '';
  document.getElementById('vaultSvcTLSSkip').checked = edit ? !!edit.tls_skip_verify : false;
  if (edit) {
    document.getElementById('vaultSvcToken').placeholder = '(unchanged -leave empty to keep)';
    document.getElementById('vaultSvcHeaderValue').placeholder = '(unchanged -leave empty to keep)';
    document.getElementById('vaultSvcPassword').placeholder = '(unchanged -leave empty to keep)';
    document.getElementById('vaultSvcOAuthClientSecret').placeholder = '(unchanged -leave empty to keep)';
    document.getElementById('vaultSvcOAuthRefreshToken').placeholder = '(unchanged -leave empty to keep)';
  } else {
    document.getElementById('vaultSvcToken').placeholder = 'Bearer token';
    document.getElementById('vaultSvcHeaderValue').placeholder = 'Value';
    document.getElementById('vaultSvcPassword').placeholder = 'Password';
    document.getElementById('vaultSvcOAuthClientSecret').placeholder = 'Client Secret';
    document.getElementById('vaultSvcOAuthRefreshToken').placeholder = 'Refresh token';
  }
  // Inject secret picker toggles for sensitive auth fields
  vaultInjectSecretPickers(edit);
  vaultToggleAuthFields();
  document.getElementById('vaultServiceModal').style.display = '';
}

// Inject secret picker tabs into service modal auth fields
function vaultInjectSecretPickers(edit) {
  const pickerFields = [
    { inputId: 'vaultSvcToken', refKey: 'token_ref', groupId: 'vaultSvcBearerGroup' },
    { inputId: 'vaultSvcHeaderValue', refKey: 'header_value_ref', groupId: 'vaultSvcHeaderGroup' },
    { inputId: 'vaultSvcPassword', refKey: 'password_ref', groupId: 'vaultSvcBasicGroup' },
  ];

  // Load service refs from localStorage (sidecar for UI state)
  let serviceRefs = {};
  try { serviceRefs = JSON.parse(localStorage.getItem('alf_vault_service_refs') || '{}'); } catch (_) {}
  const svcName = edit ? edit.name : '';

  for (const pf of pickerFields) {
    const group = document.getElementById(pf.groupId);
    // Remove any existing picker
    const existing = group.querySelector('.vault-secret-picker');
    if (existing) existing.remove();

    const input = document.getElementById(pf.inputId);
    const picker = document.createElement('div');
    picker.className = 'vault-secret-picker';

    const savedRef = svcName && serviceRefs[svcName] ? serviceRefs[svcName][pf.refKey] : '';

    picker.innerHTML =
      '<div class="vault-picker-tabs">' +
        '<button type="button" class="vault-picker-tab' + (savedRef ? '' : ' active') + '" data-mode="direct">Enter value</button>' +
        '<button type="button" class="vault-picker-tab' + (savedRef ? ' active' : '') + '" data-mode="ref">From vault secret</button>' +
      '</div>' +
      '<div class="vault-picker-ref" style="' + (savedRef ? '' : 'display:none') + '">' +
        '<select class="input vault-picker-select">' +
          '<option value="">Select secret...</option>' +
          vaultSecretsCache.map(s => '<option value="' + esc(s.name) + '"' + (s.name === savedRef ? ' selected' : '') + '>' + esc(s.name) + '</option>').join('') +
          '<option value="__new__">+ New secret...</option>' +
        '</select>' +
      '</div>';

    // Insert picker before the input
    input.parentNode.insertBefore(picker, input);

    // Tab switching
    const tabs = picker.querySelectorAll('.vault-picker-tab');
    const refDiv = picker.querySelector('.vault-picker-ref');
    tabs.forEach(tab => {
      tab.addEventListener('click', () => {
        tabs.forEach(t => t.classList.remove('active'));
        tab.classList.add('active');
        const isRef = tab.dataset.mode === 'ref';
        refDiv.style.display = isRef ? '' : 'none';
        input.style.display = isRef ? 'none' : '';
      });
    });

    // If ref mode active, hide the input
    if (savedRef) input.style.display = 'none';

    // "New secret..." option
    const sel = picker.querySelector('.vault-picker-select');
    sel.addEventListener('change', async () => {
      if (sel.value === '__new__') {
        sel.value = '';
        // Open the add secret modal, then add the new secret to dropdown
        const newResult = await vaultAddSecretModal();
        // Refresh secrets cache and repopulate
        try {
          const secrets = await api('/api/vault/secrets');
          vaultSecretsCache = (secrets || []).filter(s => !s.name.includes('.'));
        } catch (_) {}
        // Repopulate all pickers
        document.querySelectorAll('.vault-picker-select').forEach(s => {
          const currentVal = s.value;
          const optHtml = '<option value="">Select secret...</option>' +
            vaultSecretsCache.map(sc => '<option value="' + esc(sc.name) + '">' + esc(sc.name) + '</option>').join('') +
            '<option value="__new__">+ New secret...</option>';
          s.innerHTML = optHtml;
          if (currentVal) s.value = currentVal;
        });
      }
    });
  }
}

function vaultEditService(name) {
  const svc = vaultServicesCache.find(s => s.name === name);
  if (!svc) return;
  vaultShowServiceModal(svc);
}

function vaultToggleAuthFields() {
  const type = document.getElementById('vaultSvcAuthType').value;
  document.getElementById('vaultSvcBearerGroup').style.display = type === 'bearer' ? '' : 'none';
  document.getElementById('vaultSvcHeaderGroup').style.display = type === 'header' ? '' : 'none';
  document.getElementById('vaultSvcBasicGroup').style.display = type === 'basic' ? '' : 'none';
  document.getElementById('vaultSvcOAuth2Group').style.display = type === 'oauth2_client' ? '' : 'none';
  document.getElementById('vaultSvcSAGroup').style.display = type === 'service_account' ? '' : 'none';
  if (type === 'service_account') vaultPopulateFileRefs();
  if (type === 'oauth2_client') vaultPopulateOAuthFileRefs();
}

function vaultOAuth2SetMode(mode) {
  document.querySelectorAll('.oauth2-tab').forEach(t => t.classList.toggle('active', t.dataset.mode === mode));
  document.getElementById('vaultOAuth2BrowserMode').style.display = mode === 'browser' ? '' : 'none';
  document.getElementById('vaultOAuth2ManualMode').style.display = mode === 'manual' ? '' : 'none';
}

async function vaultPopulateOAuthFileRefs() {
  const sel = document.getElementById('vaultSvcOAuthFileRef');
  try {
    const files = await api('/api/vault/files');
    while (sel.options.length > 1) sel.remove(1);
    for (const f of (files || [])) {
      const name = typeof f === 'string' ? f : f.name;
      const opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      sel.appendChild(opt);
    }
  } catch {}
}

async function vaultOAuth2StartFlow() {
  const name = document.getElementById('vaultSvcName').value.trim();
  const baseURL = document.getElementById('vaultSvcBaseURL').value.trim();
  const fileRef = document.getElementById('vaultSvcOAuthFileRef').value;
  const scopesRaw = document.getElementById('vaultSvcOAuthBrowserScopes').value.trim();
  const tlsSkip = document.getElementById('vaultSvcTLSSkip').checked;
  const statusEl = document.getElementById('vaultOAuth2FlowStatus');

  if (!name || !baseURL) { alert('Name and Base URL are required'); return; }
  if (!fileRef) { alert('Select a client secret file'); return; }

  const btn = document.getElementById('vaultOAuth2AuthorizeBtn');
  btn.disabled = true;
  statusEl.textContent = 'Starting flow...';
  statusEl.style.color = 'var(--text-dim)';

  // Build the CC callback URL so Google redirects back through the Control Center.
  const ccOrigin = window.location.origin;
  const payload = {
    client_secret_file: fileRef,
    service_name: name,
    base_url: baseURL,
    redirect_uri: ccOrigin + '/api/vault/oauth2/callback',
  };
  if (scopesRaw) payload.scopes = scopesRaw.split(',').map(s => s.trim()).filter(Boolean);
  if (tlsSkip) payload.tls_skip_verify = true;

  try {
    const data = await api('/api/vault/oauth2/authorize', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (data.auth_url) {
      window.open(data.auth_url, '_blank');
      statusEl.textContent = 'Waiting for authorization... (check browser tab)';
      statusEl.style.color = 'var(--accent)';
      // Poll for service creation.
      let attempts = 0;
      const poll = setInterval(async () => {
        attempts++;
        if (attempts > 60) { // 5 min timeout
          clearInterval(poll);
          statusEl.textContent = 'Flow timed out';
          statusEl.style.color = 'var(--danger, #e55)';
          btn.disabled = false;
          return;
        }
        try {
          const services = await api('/api/vault/services');
          if ((services || []).some(s => s.name === name)) {
            clearInterval(poll);
            statusEl.textContent = 'Service created!';
            statusEl.style.color = 'var(--success, #4c4)';
            btn.disabled = false;
            document.getElementById('vaultServiceModal').style.display = 'none';
            vaultLoadServices();
            toast('OAuth2 service "' + name + '" created');
          }
        } catch {}
      }, 5000);
    } else {
      statusEl.textContent = 'Error: no auth_url returned';
      statusEl.style.color = 'var(--danger, #e55)';
      btn.disabled = false;
    }
  } catch (err) {
    statusEl.textContent = 'Error: ' + (err?.error || err?.message || 'unknown');
    statusEl.style.color = 'var(--danger, #e55)';
    btn.disabled = false;
  }
}

async function vaultSaveService() {
  const name = document.getElementById('vaultSvcName').value.trim();
  const baseURL = document.getElementById('vaultSvcBaseURL').value.trim();
  const authType = document.getElementById('vaultSvcAuthType').value;
  if (!name || !baseURL) { alert('Name and Base URL are required'); return; }

  const tlsSkip = document.getElementById('vaultSvcTLSSkip').checked;
  const payload = { name, base_url: baseURL, auth: { type: authType } };
  if (tlsSkip) payload.tls_skip_verify = true;

  // Helper: check if a field uses a secret ref instead of direct value
  function getFieldOrRef(inputId, refKey, directKey) {
    const group = document.getElementById(inputId).closest('.form-group');
    const picker = group ? group.querySelector('.vault-secret-picker') : null;
    if (picker) {
      const activeTab = picker.querySelector('.vault-picker-tab.active');
      if (activeTab && activeTab.dataset.mode === 'ref') {
        const sel = picker.querySelector('.vault-picker-select');
        if (sel && sel.value && sel.value !== '__new__') {
          payload.auth[refKey] = sel.value;
          // Save ref mapping to localStorage
          let refs = {};
          try { refs = JSON.parse(localStorage.getItem('alf_vault_service_refs') || '{}'); } catch (_) {}
          if (!refs[name]) refs[name] = {};
          refs[name][refKey] = sel.value;
          localStorage.setItem('alf_vault_service_refs', JSON.stringify(refs));
          return;
        }
      }
    }
    // Direct value
    const val = document.getElementById(inputId).value;
    if (val) payload.auth[directKey] = val;
    // Clear any stale ref
    let refs = {};
    try { refs = JSON.parse(localStorage.getItem('alf_vault_service_refs') || '{}'); } catch (_) {}
    if (refs[name]) { delete refs[name][refKey]; localStorage.setItem('alf_vault_service_refs', JSON.stringify(refs)); }
  }

  if (authType === 'bearer') {
    getFieldOrRef('vaultSvcToken', 'token_ref', 'token');
  } else if (authType === 'header') {
    payload.auth.header_name = document.getElementById('vaultSvcHeaderName').value;
    getFieldOrRef('vaultSvcHeaderValue', 'header_value_ref', 'header_value');
  } else if (authType === 'basic') {
    payload.auth.username = document.getElementById('vaultSvcUsername').value;
    getFieldOrRef('vaultSvcPassword', 'password_ref', 'password');
  } else if (authType === 'oauth2_client') {
    const oauth2Mode = document.querySelector('.oauth2-tab.active')?.dataset.mode || 'manual';
    if (oauth2Mode === 'browser') {
      alert('Use the "Authorize in Browser" button for browser flow');
      return;
    }
    const cid = document.getElementById('vaultSvcOAuthClientId').value;
    const csec = document.getElementById('vaultSvcOAuthClientSecret').value;
    const turl = document.getElementById('vaultSvcOAuthTokenUrl').value;
    const rtok = document.getElementById('vaultSvcOAuthRefreshToken').value;
    if (cid) payload.auth.client_id = cid;
    if (csec) payload.auth.client_secret = csec;
    if (turl) payload.auth.token_url = turl;
    if (rtok) payload.auth.refresh_token = rtok;
    const scopes = document.getElementById('vaultSvcOAuthScopes').value.trim();
    if (scopes) payload.auth.scopes = scopes.split(',').map(s => s.trim()).filter(Boolean);
  } else if (authType === 'service_account') {
    payload.auth.file_ref = document.getElementById('vaultSvcSAFileRef').value;
    if (!payload.auth.file_ref) { alert('Select a key file'); return; }
    const saScopes = document.getElementById('vaultSvcSAScopes').value.trim();
    if (saScopes) payload.auth.sa_scopes = saScopes.split(',').map(s => s.trim()).filter(Boolean);
    const saTokenUrl = document.getElementById('vaultSvcSATokenUrl').value.trim();
    if (saTokenUrl) payload.auth.sa_token_url = saTokenUrl;
  }

  try {
    await api('/api/vault/services', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    document.getElementById('vaultServiceModal').style.display = 'none';
    vaultLoadServices();
  } catch (err) {
    alert('Save failed: ' + (err?.error || err?.message || 'unknown error'));
  }
}

const vaultScopeLabels = {
  proxy: 'LLM Access (read-only)',
  admin: 'Full Access',
};

async function vaultLoadTokens() {
  try {
    const tokens = await api('/api/vault/tokens');
    const list = document.getElementById('vaultTokensList');
    if (!tokens || tokens.length === 0) {
      list.innerHTML = '<div class="vault-empty">No active keys.</div>';
      return;
    }
    list.innerHTML = tokens.map(t => {
      const prefix = t.id_prefix || t.id || '???';
      const label = vaultScopeLabels[t.scope] || esc(t.scope);
      const isSystem = t.scope === 'admin';
      return `
      <div class="vault-item">
        <div class="vault-item-info">
          <span class="vault-item-name">${esc(prefix)}${isSystem ? ' <span class="vault-system-badge">system</span>' : ''}</span>
          <span class="vault-item-detail">${label}</span>
        </div>
        <div class="vault-item-actions">
          ${isSystem ? '' : `<button class="btn btn-icon vault-revoke-btn" data-prefix="${esc(prefix)}" title="Revoke"><i data-lucide="trash-2"></i></button>`}
        </div>
      </div>`;
    }).join('');
    lucide.createIcons();
    list.querySelectorAll('.vault-revoke-btn').forEach(btn => {
      btn.addEventListener('click', () => vaultRevokeKey(btn.dataset.prefix));
    });
  } catch (err) {
    document.getElementById('vaultTokensList').innerHTML = '<div class="vault-empty">Error loading keys</div>';
  }
}

async function vaultCreateTokenModal() {
  const result = await vaultModal({
    title: 'Create Access Key',
    fields: [
      { id: 'scope', label: 'Scope', type: 'select', value: 'proxy', options: [
        { value: 'proxy', label: 'LLM Access (read-only)' },
        { value: 'admin', label: 'Full Access' },
      ]},
    ],
    saveLabel: 'Create',
  });
  if (!result) return;
  try {
    const res = await api('/api/vault/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ scope: result.scope })
    });
    if (res.id) {
      await vaultModal({
        title: 'Access Key Created',
        fields: [
          { id: 'key', label: 'Key (copy now - shown only once)', type: 'text', value: res.id, disabled: true },
        ],
        saveLabel: 'Done',
      });
    }
    vaultLoadTokens();
  } catch (err) {
    toast('Create key failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
  }
}

async function vaultRevokeKey(prefix) {
  if (!confirm('Revoke this key?')) return;
  try {
    const cleanId = prefix.replace(/\.+$/, ''); // strip trailing dots from "abc123..."
    await api('/api/vault/tokens/' + encodeURIComponent(cleanId), { method: 'DELETE' });
    vaultLoadTokens();
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    alert('Revoke failed: ' + msg);
  }
}

// --- Vault Export / Import ---

async function vaultExportModal() {
  const result = await vaultModal({
    title: 'Export Vault',
    fields: [
      { id: 'info', type: 'info', value: 'Your vault will be encrypted with the password you provide.' },
      { id: 'password', label: 'Password', type: 'password', placeholder: 'Encryption password' },
      { id: 'confirm', label: 'Confirm password', type: 'password', placeholder: 'Repeat password' },
    ],
    saveLabel: 'Export',
  });
  if (!result) return;
  const pw = (result.password || '').trim();
  const pw2 = (result.confirm || '').trim();
  if (!pw) { toast('Password required', 'error'); return; }
  if (pw !== pw2) { toast('Passwords do not match', 'error'); return; }
  try {
    const resp = await _nativeFetch('/api/vault/export', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
      credentials: 'same-origin',
      body: JSON.stringify({ password: pw }),
    });
    if (!resp.ok) {
      const err = await resp.json();
      throw err;
    }
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'vault-export.enc';
    a.click();
    URL.revokeObjectURL(url);
    toast('Vault exported (encrypted)');
  } catch (err) {
    toast('Export failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
  }
}

async function vaultImportModal() {
  const input = document.getElementById('vaultImportFile');
  const file = input.files[0];
  if (!file) return;
  input.value = '';

  const isEncrypted = file.name.endsWith('.enc');

  if (isEncrypted) {
    const result = await vaultModal({
      title: 'Import Encrypted Vault',
      fields: [
        { id: 'info', type: 'info', value: 'This file is encrypted. Enter the password used during export.' },
        { id: 'password', label: 'Password', type: 'password', placeholder: 'Decryption password' },
      ],
      saveLabel: 'Import',
    });
    if (!result) return;
    const pw = (result.password || '').trim();
    if (!pw) { toast('Password required', 'error'); return; }
    try {
      const buf = await file.arrayBuffer();
      const b64 = btoa(String.fromCharCode(...new Uint8Array(buf)));
      const res = await api('/api/vault/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ data: b64, password: pw }),
      });
      toast('Imported ' + (res.imported || 0) + ' secrets' + (res.services_imported ? ', ' + res.services_imported + ' services' : ''));
      vaultLoadSecrets();
      vaultLoadFiles();
      vaultLoadServices();
    } catch (err) {
      toast('Import failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
    }
  } else {
    // Plain JSON import (backward compat)
    try {
      const text = await file.text();
      const data = JSON.parse(text);
      if (!data.secrets || !Array.isArray(data.secrets)) {
        toast('Invalid file: missing "secrets" array', 'error');
        return;
      }
      if (!confirm('Import ' + data.secrets.length + ' secrets? Existing secrets with the same name will be overwritten.')) return;
      const res = await api('/api/vault/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: text,
      });
      toast('Imported ' + (res.imported || 0) + ' secrets' + (res.services_imported ? ', ' + res.services_imported + ' services' : ''));
      vaultLoadSecrets();
      vaultLoadFiles();
      vaultLoadServices();
    } catch (err) {
      toast('Import failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
    }
  }
}

// --- Vault Secrets (API Keys) ---

async function vaultLoadSecrets() {
  try {
    const secrets = await api('/api/vault/secrets');
    const list = document.getElementById('vaultSecretsList');
    // Filter: secrets are files without a file extension (api keys, tokens).
    const filtered = (secrets || []).filter(s => !s.name.includes('.'));
    vaultSecretsCache = filtered;
    if (filtered.length === 0) {
      list.innerHTML = '<div class="vault-empty">No secrets stored. Add API keys for your backends here.</div>';
      return;
    }
    // Detect backend-linked secrets
    const backendNames = BACKENDS.filter(b => b);
    list.innerHTML = filtered.map(s => {
      const linkedBackend = backendNames.find(b => s.name === b + '_api_key');
      const badge = linkedBackend
        ? ' <span class="vault-backend-badge">' + esc(linkedBackend) + '</span>'
        : '';
      return `
      <div class="vault-item">
        <div class="vault-item-info">
          <span class="vault-item-name">${esc(s.name)}${badge}</span>
        </div>
        <div class="vault-item-actions">
          <button class="btn btn-icon vault-secret-del-btn" data-name="${esc(s.name)}" title="Delete"><i data-lucide="trash-2"></i></button>
        </div>
      </div>`;
    }).join('');
    lucide.createIcons();
    list.querySelectorAll('.vault-secret-del-btn').forEach(btn => {
      btn.addEventListener('click', () => vaultDeleteSecret(btn.dataset.name));
    });
  } catch (err) {
    document.getElementById('vaultSecretsList').innerHTML = '<div class="vault-empty">Error loading secrets</div>';
  }
}

async function vaultAddSecretModal() {
  const backendNames = BACKENDS.filter(b => b);
  const backendOpts = [{ value: '', label: 'None (manual name)' }].concat(
    backendNames.map(b => ({ value: b, label: b + ' API key' }))
  );

  const fields = [
    { id: 'backend', label: 'Link to backend', type: 'select', options: backendOpts,
      onChange: function(val) {
        const nameEl = document.getElementById('vm_name');
        if (val) {
          nameEl.value = val + '_api_key';
          nameEl.readOnly = true;
        } else {
          nameEl.value = '';
          nameEl.readOnly = false;
        }
      }
    },
    { id: 'name', label: 'Name', type: 'text', placeholder: 'e.g. openrouter_api_key' },
    { id: 'value', label: 'Value', type: 'password', placeholder: 'sk-or-...' },
  ];

  const result = await vaultModal({ title: 'Add Secret', fields, saveLabel: 'Save' });
  if (!result) return;
  const name = (result.name || '').trim();
  const value = (result.value || '').trim();
  if (!name || !value) { toast('Name and value are required', 'error'); return; }
  try {
    await api('/api/vault/secrets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, value })
    });
    toast('Secret saved');
    vaultLoadSecrets();
  } catch (err) {
    toast('Save failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
  }
}

async function vaultDeleteSecret(name) {
  if (!confirm('Delete secret "' + name + '"?\n\nBackends using this key will stop working.')) return;
  try {
    await api('/api/vault/secrets/' + encodeURIComponent(name), { method: 'DELETE' });
    vaultLoadSecrets();
  } catch (err) {
    toast('Delete failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
  }
}

// --- Vault Files ---

let vaultFilesCache = [];

async function vaultLoadFiles() {
  try {
    const files = await api('/api/vault/files');
    vaultFilesCache = files || [];
    const list = document.getElementById('vaultFilesList');
    if (!files || files.length === 0) {
      list.innerHTML = '<div class="vault-empty">No files stored. Upload service account keys or certificates here.</div>';
      return;
    }
    // Filter: files are entries with a file extension (exclude plain secrets).
    const realFiles = files.filter(f => f.name.includes('.'));
    if (realFiles.length === 0) {
      list.innerHTML = '<div class="vault-empty">No files stored. Upload service account keys or certificates here.</div>';
      return;
    }
    list.innerHTML = realFiles.map(f => `
      <div class="vault-item">
        <div class="vault-item-info">
          <span class="vault-item-name">${esc(f.name)}</span>
          <span class="vault-item-detail">${esc(f.mime_type)} &middot; ${formatFileSize(f.size)}</span>
        </div>
        <div class="vault-item-actions">
          <button class="btn btn-icon vault-file-del-btn" data-name="${esc(f.name)}" title="Delete"><i data-lucide="trash-2"></i></button>
        </div>
      </div>
    `).join('');
    lucide.createIcons();
    list.querySelectorAll('.vault-file-del-btn').forEach(btn => {
      btn.addEventListener('click', () => vaultDeleteFile(btn.dataset.name));
    });
  } catch (err) {
    document.getElementById('vaultFilesList').innerHTML = '<div class="vault-empty">Error loading files</div>';
  }
}

function formatFileSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

async function vaultUploadFileModal() {
  const input = document.getElementById('vaultFileInput');
  const file = input.files[0];
  if (!file) return;
  input.value = '';

  const result = await vaultModal({
    title: 'Upload File',
    fields: [
      { id: 'name', label: 'File name in vault', type: 'text', value: file.name, placeholder: 'e.g. credentials.json' },
    ],
    saveLabel: 'Upload',
  });
  if (!result) return;
  const name = (result.name || '').trim();
  if (!name) { toast('Name required', 'error'); return; }

  const form = new FormData();
  form.append('name', name);
  form.append('file', file);

  try {
    await _nativeFetch('/api/vault/files', {
      method: 'POST',
      headers: { 'X-Requested-With': 'XMLHttpRequest' },
      credentials: 'same-origin',
      body: form
    }).then(r => {
      if (!r.ok) return r.json().then(j => { throw j; });
      return r.json();
    });
    toast('File uploaded');
    vaultLoadFiles();
  } catch (err) {
    toast('Upload failed: ' + (err?.error || err?.message || 'unknown error'), 'error');
  }
}

async function vaultDeleteFile(name) {
  if (!confirm('Delete file "' + name + '"?\n\nServices referencing this file will break.')) return;
  try {
    await api('/api/vault/files/' + encodeURIComponent(name), { method: 'DELETE' });
    vaultLoadFiles();
  } catch (err) {
    alert('Delete failed: ' + (err?.error || err?.message || 'unknown error'));
  }
}

// --- Skills Store ---
(function() {
  const STORE_PREF_KEY = 'alf_skill_import_prefs';
  const phase1 = document.getElementById('skillImportPhase1');
  const phase2 = document.getElementById('skillImportPhase2');
  const loading = document.getElementById('skillImportLoading');
  const cmdInput = document.getElementById('skillImportCmd');
  const backendSelect = document.getElementById('skillImportBackend');
  const modelSelect = document.getElementById('skillImportModel');
  const rememberCheck = document.getElementById('skillImportRemember');

  let scanData = null;

  // Populate backend/model on page load.
  function initSkillStore() {
    backendSelect.innerHTML = BACKENDS.map(b =>
      '<option value="' + b + '">' + (b || 'claude (default)') + '</option>'
    ).join('');
    // Restore saved preferences.
    let savedPrefs = null;
    try {
      savedPrefs = JSON.parse(localStorage.getItem(STORE_PREF_KEY));
      if (savedPrefs) {
        if (savedPrefs.backend && BACKENDS.includes(savedPrefs.backend)) backendSelect.value = savedPrefs.backend;
        rememberCheck.checked = true;
      }
    } catch (_) {}
    fetchBackendModels(backendSelect.value, 'skillImportModel');
    // Restore saved model once the dropdown is populated (poll up to 3s).
    // renderModelField replaces the DOM, so re-query the element each attempt.
    if (savedPrefs && savedPrefs.model) {
      let attempts = 0;
      const tryRestore = setInterval(() => {
        attempts++;
        const sel = document.getElementById('skillImportModel');
        if (sel && sel.querySelector('option[value="' + savedPrefs.model + '"]')) {
          sel.value = savedPrefs.model;
          modelSelect = sel;
          clearInterval(tryRestore);
        } else if (attempts > 15) {
          clearInterval(tryRestore);
        }
      }, 200);
    }
  }
  // Init after tiers are loaded (BACKENDS populated).
  setTimeout(initSkillStore, 800);

  backendSelect.addEventListener('change', function() {
    fetchBackendModels(this.value, 'skillImportModel');
    if (rememberCheck.checked) {
      localStorage.setItem(STORE_PREF_KEY, JSON.stringify({ backend: backendSelect.value, model: modelSelect.value }));
    }
  });

  modelSelect.addEventListener('change', function() {
    if (rememberCheck.checked) {
      localStorage.setItem(STORE_PREF_KEY, JSON.stringify({ backend: backendSelect.value, model: modelSelect.value }));
    }
  });

  rememberCheck.addEventListener('change', function() {
    if (this.checked) {
      localStorage.setItem(STORE_PREF_KEY, JSON.stringify({ backend: backendSelect.value, model: modelSelect.value }));
    } else {
      localStorage.removeItem(STORE_PREF_KEY);
    }
  });

  function resetToPhase1() {
    scanData = null;
    phase1.style.display = '';
    phase2.style.display = 'none';
    loading.style.display = 'none';
  }

  document.getElementById('skillImportCancel2').addEventListener('click', resetToPhase1);

  document.getElementById('skillImportScan').addEventListener('click', async () => {
    const cmd = cmdInput.value.trim();
    if (!cmd) { toast('Paste a skills.sh command or owner/repo', 'error'); return; }

    phase1.style.display = 'none';
    loading.style.display = 'flex';

    // Save preferences if checked.
    if (rememberCheck.checked) {
      localStorage.setItem(STORE_PREF_KEY, JSON.stringify({ backend: backendSelect.value, model: modelSelect.value }));
    } else {
      localStorage.removeItem(STORE_PREF_KEY);
    }

    try {
      const data = await api('/api/skills/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'scan',
          command: cmd,
          backend: backendSelect.value,
          model: modelSelect.value
        })
      });
      scanData = data;
      showReview(data);
    } catch (err) {
      loading.style.display = 'none';
      phase1.style.display = '';
      // If repo has multiple skills, show picker.
      if (err.available_skills && err.available_skills.length > 0) {
        const skills = err.available_skills;
        const hint = err.hint || '';
        const listHTML = skills.map(s =>
          '<button class="btn btn-sm skill-pick-btn" data-skill="' + esc(s) + '">' + esc(s) + '</button>'
        ).join(' ');
        const pickerHTML = '<div class="skill-picker">' +
          '<p><strong>This repo contains multiple skills.</strong> Pick one:</p>' +
          '<div class="skill-pick-list">' + listHTML + '</div></div>';
        // Insert picker above the command input.
        let picker = document.getElementById('skillPickerWrap');
        if (!picker) {
          picker = document.createElement('div');
          picker.id = 'skillPickerWrap';
          cmdInput.parentNode.insertBefore(picker, cmdInput);
        }
        picker.innerHTML = pickerHTML;
        picker.querySelectorAll('.skill-pick-btn').forEach(btn => {
          btn.addEventListener('click', () => {
            const base = cmdInput.value.replace(/\s+--skill\s+\S+/, '').trim();
            cmdInput.value = base + ' --skill ' + btn.dataset.skill;
            picker.innerHTML = '';
            toast('Selected "' + btn.dataset.skill + '" -click Scan & Review');
          });
        });
        if (window.lucide) lucide.createIcons();
      } else {
        toast(err.error || 'Scan failed', 'error');
      }
    }
  });

  function showReview(data) {
    loading.style.display = 'none';
    phase2.style.display = '';

    document.getElementById('skillImportName').textContent = data.name;
    document.getElementById('skillImportSource').textContent = data.source;
    document.getElementById('skillImportDesc').textContent = data.description || '-';

    const badge = document.getElementById('skillImportVerdict');
    badge.textContent = data.verdict;
    badge.className = 'verdict-badge verdict-' + data.verdict.toLowerCase();

    const issuesEl = document.getElementById('skillImportIssues');
    if (data.issues && data.issues.length > 0) {
      issuesEl.style.display = '';
      issuesEl.innerHTML = '<strong>Issues found:</strong><ul>' +
        data.issues.map(i => '<li>' + esc(i) + '</li>').join('') + '</ul>';
    } else {
      issuesEl.style.display = 'none';
    }

    document.getElementById('skillImportTriggers').value = (data.triggers || []).join(', ');

    // Populate tier dropdown from tiersCache.
    const tierSelect = document.getElementById('skillImportTier');
    let tierOpts = '<option value="">(any tier)</option>';
    if (tiersCache && tiersCache.tiers) {
      tiersCache.tiers.filter(t => t.enabled).forEach(t => {
        tierOpts += '<option value="' + esc(t.name) + '"' + (t.name === data.tier ? ' selected' : '') + '>' + esc(t.name) + '</option>';
      });
    }
    tierSelect.innerHTML = tierOpts;

    document.getElementById('skillImportContent').value = data.content;

    const installBtn = document.getElementById('skillImportInstall');
    if (data.verdict === 'FAIL') {
      installBtn.textContent = 'Install Anyway';
      installBtn.classList.add('btn-danger');
    } else if (data.verdict === 'WARN') {
      installBtn.textContent = 'Install Anyway';
      installBtn.classList.remove('btn-danger');
    } else {
      installBtn.textContent = 'Install';
      installBtn.classList.remove('btn-danger');
    }
  }

  // Disclaimer toggle.
  document.getElementById('skillStoreInfoBtn').addEventListener('click', () => {
    const d = document.getElementById('skillStoreDisclaimer');
    d.style.display = d.style.display === 'none' ? '' : 'none';
  });

  // Correct with AI.
  document.getElementById('skillImportCorrect').addEventListener('click', async () => {
    if (!scanData) return;
    const btn = document.getElementById('skillImportCorrect');
    btn.disabled = true;
    btn.innerHTML = '<div class="spinner" style="width:13px;height:13px"></div> Correcting...';

    const issues = (scanData.issues || []).join('\n- ');
    try {
      const data = await api('/api/skills/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'correct',
          content: document.getElementById('skillImportContent').value,
          triggers: issues, // pass issues via triggers field
          backend: backendSelect.value,
          model: modelSelect.value
        })
      });
      document.getElementById('skillImportContent').value = data.content;
      scanData.content = data.content;
      toast('Skill corrected -review the changes before installing');
    } catch (err) {
      toast(err.error || 'Correction failed', 'error');
    } finally {
      btn.disabled = false;
      btn.innerHTML = '<i data-lucide="wand-2" style="width:13px;height:13px"></i> Correct with AI';
      if (window.lucide) lucide.createIcons();
    }
  });

  document.getElementById('skillImportInstall').addEventListener('click', async () => {
    if (!scanData) return;

    const installBtn = document.getElementById('skillImportInstall');
    installBtn.disabled = true;
    installBtn.textContent = 'Installing...';

    try {
      const data = await api('/api/skills/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'install',
          name: scanData.name,
          content: document.getElementById('skillImportContent').value,
          triggers: document.getElementById('skillImportTriggers').value,
          tier: document.getElementById('skillImportTier').value,
          source: scanData.source
        })
      });
      toast('Skill "' + data.name + '" installed successfully');
      resetToPhase1();
      cmdInput.value = '';
      wsInit();
    } catch (err) {
      toast(err.error || 'Install failed', 'error');
    } finally {
      installBtn.disabled = false;
      installBtn.textContent = 'Install';
    }
  });
})();

async function vaultPopulateFileRefs() {
  const select = document.getElementById('vaultSvcSAFileRef');
  const current = select.value;
  // Use cached files or fetch fresh.
  if (!vaultFilesCache.length) {
    try { vaultFilesCache = await api('/api/vault/files') || []; } catch (e) { /* ignore */ }
  }
  select.innerHTML = '<option value="">-- select key file --</option>' +
    vaultFilesCache.map(f => `<option value="${esc(f.name)}">${esc(f.name)}</option>`).join('');
  if (current) select.value = current;
}

// --- Marketplace ---
var mpInitialized = false;

function mpInit() {
  if (!mpInitialized) {
    mpInitialized = true;
    document.getElementById('mpRefreshBtn').addEventListener('click', mpLoad);
  }
  mpLoad();
}

function mpLoad() {
  const grid = document.getElementById('mpGrid');
  grid.innerHTML = '<div style="color:var(--text-dim);font-size:0.85rem;padding:8px">Loading...</div>';

  // Fetch local state, remote catalog, and updates in parallel.
  Promise.all([
    api('/api/marketplace').catch(() => []),
    api('/api/marketplace/catalog').catch(() => []),
    api('/api/marketplace/updates').catch(() => [])
  ]).then(([localApps, remoteApps, updates]) => {
    // Build update map: slug → remote_version
    var updateMap = {};
    (updates || []).forEach(function(u) { updateMap[u.slug] = u.remote_version; });
    // Build local state map: slug → app info
    const localMap = {};
    (localApps || []).forEach(a => { localMap[a.slug] = a; });

    // Merge: remote apps enriched with local state, plus local-only apps
    const merged = [];
    const seen = {};

    // Remote apps first
    (remoteApps || []).forEach(remote => {
      seen[remote.slug] = true;
      const local = localMap[remote.slug];
      merged.push({
        slug: remote.slug,
        name: remote.name,
        version: remote.version,
        description: remote.description,
        author: remote.author,
        category: remote.category,
        icon: remote.icon,
        tools: local ? local.tools : [],
        state: local ? local.state : 'available',
        source: 'remote'
      });
    });

    // Local-only apps are NOT shown in marketplace — they appear in the sidebar only.

    if (merged.length === 0) {
      grid.innerHTML = '<div class="mp-empty">No apps available yet.</div>';
      return;
    }

    // Sort: by category then alphabetical by name.
    merged.sort((a, b) => {
      var catA = (a.category || 'other').toLowerCase();
      var catB = (b.category || 'other').toLowerCase();
      if (catA !== catB) return catA.localeCompare(catB);
      return (a.name || a.slug || '').localeCompare(b.name || b.slug || '');
    });

    grid.innerHTML = '';
    var lastCategory = null;
    merged.forEach(app => {
      var cat = (app.category || 'Other');
      if (cat !== lastCategory) {
        lastCategory = cat;
        var heading = document.createElement('div');
        heading.className = 'mp-category-heading';
        heading.textContent = cat.charAt(0).toUpperCase() + cat.slice(1);
        grid.appendChild(heading);
      }
      const card = document.createElement('div');
      card.className = 'mp-card';
      if (app.state === 'enabled') card.classList.add('mp-card-enabled');

      const iconName = app.icon || 'package';
      const toolCount = (app.tools || []).length;
      const isAvailable = app.state === 'available';
      const hasUpdate = !!updateMap[app.slug];
      const stateLabel = isAvailable ? 'Available' : app.state === 'enabled' ? 'Enabled' : app.state === 'disabled' ? 'Disabled' : 'Installed';
      const stateCls = 'mp-state-' + app.state;

      card.innerHTML =
        '<div class="mp-card-header">' +
          '<div class="mp-card-icon"><i data-lucide="' + esc(iconName) + '"></i></div>' +
          '<div class="mp-card-info">' +
            '<div class="mp-card-title">' + esc(app.name) + '</div>' +
            '<div class="mp-card-meta">' +
              '<span class="mp-badge ' + stateCls + '">' + stateLabel + '</span>' +
              '<span class="mp-card-version">v' + esc(app.version || '?') + '</span>' +
              (app.author ? '<span class="mp-card-cat">by ' + esc(app.author) + '</span>' : '') +
              (app.category ? '<span class="mp-card-cat">' + esc(app.category) + '</span>' : '') +
            '</div>' +
          '</div>' +
        '</div>' +
        '<p class="mp-card-desc">' + esc(app.description || '') + '</p>' +
        (toolCount > 0 ? '<div class="mp-card-tools">' + (app.tools || []).map(t =>
          '<span class="mp-tool-tag" title="' + esc(t.description || '') + '">' + esc(t.name) + '</span>'
        ).join('') + '</div>' : '') +
        '<div class="mp-card-actions">' +
          (isAvailable
            ? '<button class="btn btn-sm btn-primary mp-install-btn" data-slug="' + esc(app.slug) + '">Install</button>'
            : app.state === 'enabled'
              ? '<button class="btn btn-sm mp-disable-btn" data-slug="' + esc(app.slug) + '">Disable</button>'
              : '<button class="btn btn-sm btn-primary mp-enable-btn" data-slug="' + esc(app.slug) + '">Enable</button>' +
                '<button class="btn btn-sm btn-ghost mp-uninstall-btn" data-slug="' + esc(app.slug) + '">Uninstall</button>') +
          (hasUpdate
            ? '<button class="btn btn-sm mp-update-btn" data-slug="' + esc(app.slug) + '" style="border-color:var(--accent);color:var(--accent)">Update to v' + esc(updateMap[app.slug]) + '</button>'
            : '') +
        '</div>';

      grid.appendChild(card);
    });

    // Bind actions
    grid.querySelectorAll('.mp-update-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        btn.disabled = true;
        btn.textContent = 'Updating...';
        mpAction(btn.dataset.slug, 'update');
      });
    });
    grid.querySelectorAll('.mp-install-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        btn.disabled = true;
        btn.textContent = 'Installing...';
        mpAction(btn.dataset.slug, 'install');
      });
    });
    grid.querySelectorAll('.mp-enable-btn').forEach(btn => {
      btn.addEventListener('click', () => mpAction(btn.dataset.slug, 'enable'));
    });
    grid.querySelectorAll('.mp-disable-btn').forEach(btn => {
      btn.addEventListener('click', () => mpAction(btn.dataset.slug, 'disable'));
    });
    grid.querySelectorAll('.mp-uninstall-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        if (confirm('Uninstall ' + btn.dataset.slug + '? App data will be preserved.')) {
          mpAction(btn.dataset.slug, 'uninstall');
        }
      });
    });

    if (window.lucide) lucide.createIcons();
  }).catch(e => {
    grid.innerHTML = '<div class="mp-empty">Failed to load marketplace.</div>';
  });
}

function mpAction(slug, action) {
  api('/api/marketplace/' + slug + '/' + action, { method: 'POST' })
    .then(() => {
      toast(slug + ' ' + action + 'd', 'success');
      mpLoad();
      loadApps(); // refresh sidebar
      mpCheckBadge();
    })
    .catch(e => {
      toast((e && e.error) || 'Action failed', 'error');
    });
}

// Badge: red dot on Marketplace nav when updates are available.
function mpCheckBadge() {
  api('/api/marketplace/updates').then(function(updates) {
    var navEl = document.querySelector('#navGrid .nav-item[data-view="marketplace"]');
    if (navEl) navEl.classList.toggle('has-badge', updates && updates.length > 0);
  }).catch(function() {});
}

