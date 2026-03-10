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
      toast('Session expired — send /login to your bot', 'error');
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
  setTimeout(() => el.className = 'toast', 3000);
}

// --- Theme toggle ---
// Inject theme.css into iframe when it loads an app page.
function syncIframeTheme() {
  const frame = document.getElementById('pageFrame');
  try {
    const doc = frame.contentDocument;
    if (!doc || !doc.documentElement) return;
    if (!doc.getElementById('alf-theme')) {
      const link = doc.createElement('link');
      link.id = 'alf-theme';
      link.rel = 'stylesheet';
      link.href = '/static/theme.css';
      doc.head.appendChild(link);
    }
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
  const pageFrame = document.getElementById('pageFrame');
  const docsView = document.getElementById('docsView');
  const logsView = document.getElementById('logsView');
  const tiersView = document.getElementById('tiersView');
  const firewallView = document.getElementById('firewallView');
  const vaultView = document.getElementById('vaultView');
  const terminalView = document.getElementById('terminalView');

  // Update active nav item — docs:id should highlight the docs nav item
  const navView = view.startsWith('docs:') ? 'docs' : (view.startsWith('page:') ? view : view);
  logsStopAutoRefresh();
  tasksStopAutoRefresh();
  fwStopAutoRefresh();
  document.querySelectorAll('#navGrid .nav-icon, #navPagesSection .nav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.view === navView);
  });

  homeView.style.display = 'none';
  chatView.style.display = 'none';
  schedulesView.style.display = 'none';
  tasksView.style.display = 'none';
  pageFrame.style.display = 'none';
  docsView.style.display = 'none';
  logsView.style.display = 'none';
  tiersView.style.display = 'none';
  firewallView.style.display = 'none';
  vaultView.style.display = 'none';
  terminalView.style.display = 'none';

  if (view === 'home') {
    homeView.style.display = '';
    pageFrame.src = '';
  } else if (view === 'chat') {
    chatView.style.display = '';
    pageFrame.src = '';
    chatLoadHistory();
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
  }

  localStorage.setItem('alf-view', view);

  // Close sidebar on mobile
  sidebar.classList.remove('open');
  sidebarOverlay.classList.remove('open');
}

// Bind system nav icons
document.querySelectorAll('#navGrid .nav-icon').forEach(el => {
  el.addEventListener('click', () => navigateTo(el.dataset.view));
});

// --- Status ---
function loadStatus() {
  api('/api/status').catch(() => {});
}


// --- Admin Actions ---
(function() {
  const restartBtn = document.getElementById('adminRestartBtn');
  const bootstrapBtn = document.getElementById('adminBootstrapBtn');
  const claudeAuthBtn = document.getElementById('adminClaudeAuthBtn');
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
      showOutput('Restarting... the page will reload shortly.');
      setTimeout(() => location.reload(), 4000);
    } catch (e) {
      showOutput('Restart request sent. Reloading...');
      setTimeout(() => location.reload(), 4000);
    }
  });

  bootstrapBtn.addEventListener('click', async () => {
    if (!confirm('Run bootstrap.sh? This may install packages and take a while.')) return;
    bootstrapBtn.disabled = true;
    showOutput('Running bootstrap.sh...');
    try {
      const r = await api('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: 'bash /home/alf/data/bootstrap.sh 2>&1' })
      });
      showOutput(r.output || (r.exit_code === 0 ? 'Done.' : 'Failed (exit ' + r.exit_code + ')'));
      if (r.exit_code !== 0 && r.error) showOutput(r.output + '\n\nError: ' + r.error);
    } catch (e) {
      showOutput('Error: ' + (e.message || e.error || 'request failed'));
    } finally {
      bootstrapBtn.disabled = false;
    }
  });

  claudeAuthBtn.addEventListener('click', async () => {
    claudeAuthBtn.disabled = true;
    showOutput('Checking Claude auth status...');
    try {
      const r = await api('/api/bash', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: 'HOME=/home/alf claude -p "ping" --output-format json --max-turns 1 --model haiku --allowedTools "" 2>&1 | head -5' })
      });
      if (r.exit_code === 0) {
        showOutput('Claude is authenticated.');
      } else {
        showOutput('Claude is NOT authenticated.\n\n' +
          'To authenticate, run on the host machine:\n  alf login\n\n' +
          'Or inside the container:\n  docker exec -it -e HOME=/home/alf alf claude\n  Then type /login');
      }
    } catch (e) {
      showOutput('Error: ' + (e.message || e.error || 'request failed'));
    } finally {
      claudeAuthBtn.disabled = false;
    }
  });
})();

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

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

function wsIcon(name, cls) {
  return '<i data-lucide="' + name + '"' + (cls ? ' class="' + cls + '"' : '') + '></i>';
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

function wsResetViewer() {
  wsViewMode = false;
  const viewBtn = document.getElementById('wsViewBtn');
  const viewer = document.getElementById('wsViewer');
  viewBtn.style.display = 'none';
  viewer.style.display = 'none';
  viewer.innerHTML = '';
  jvLiveData = null;
  viewBtn.innerHTML = '<i data-lucide="sliders"></i>';
  viewBtn.title = 'Form view';
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

// --- Chat ---
let chatHistoryLoaded = false;
let chatSending = false;
let chatJobId = null;
let chatEventOffset = 0;
let chatReconnectTimer = null;
const chatMessages = document.getElementById('chatMessages');
const chatInput = document.getElementById('chatInput');
const chatSendBtn = document.getElementById('chatSendBtn');
const chatStatus = document.getElementById('chatStatus');

function chatLoadHistory() {
  if (chatHistoryLoaded) return;
  chatHistoryLoaded = true;
  fetch('/api/chat?limit=50', { credentials: 'same-origin' })
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
    .then(() => chatCheckActiveJob())
    .catch(() => {});
}

// Check for in-flight background job on page load / reconnect.
async function chatCheckActiveJob() {
  try {
    const res = await fetch('/api/chat/job', { credentials: 'same-origin' });
    const data = await res.json();
    if (data.active) {
      chatJobId = data.job_id;
      chatEventOffset = 0;
      chatSending = true;
      chatSendBtn.disabled = true;
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
      // Job gone (server restart, expired) — clean up.
      chatJobId = null;
      return;
    }
    await chatProcessStream(res);
  } catch (e) {
    // Network error — auto-reconnect after delay.
    if (chatJobId) {
      await new Promise(r => setTimeout(r, 2000));
      if (chatJobId) return chatStreamFromJob(chatJobId, chatEventOffset);
    }
  }
}

const QUICK_REACTIONS = ['👍', '❤', '🔥', '😁', '🤔', '👎'];

// Legacy state vars removed — see chatThinkingEl, chatCurrentToolBlock, chatAgentTracker below.

// --- Thinking blocks (legacy-style <details> disclosures) ---

function chatNewThinkingBlock() {
  const det = document.createElement('details');
  det.className = 'chat-thinking-block';
  det.open = true;
  const summary = document.createElement('summary');
  summary.className = 'chat-thinking-summary';
  summary.textContent = '🧠 Thinking...';
  const content = document.createElement('div');
  content.className = 'chat-thinking-content';
  det.appendChild(summary);
  det.appendChild(content);
  chatMessages.appendChild(det);
  chatThinkingEl = { det: det, summary: summary, content: content, text: '' };
  chatScrollBottom();
  return chatThinkingEl;
}

function chatAppendThinkingText(text) {
  if (!chatThinkingEl) chatNewThinkingBlock();
  chatThinkingEl.text += text;
  chatThinkingEl.content.textContent = chatThinkingEl.text;
  var preview = chatThinkingEl.text.slice(0, 100).replace(/\n/g, ' ');
  chatThinkingEl.summary.textContent = '🧠 ' + preview + (chatThinkingEl.text.length > 100 ? '…' : '');
  chatThinkingEl.content.scrollTop = chatThinkingEl.content.scrollHeight;
  chatScrollBottom();
}

// --- Tool blocks (inline bubbles with name + input + result) ---

function chatNewToolBlock(name) {
  // Close previous thinking block when a tool starts
  if (chatThinkingEl && chatThinkingEl.det.open) {
    chatThinkingEl.det.open = false;
  }
  var el = document.createElement('div');
  el.className = 'chat-tool-block';
  el.innerHTML = '<div class="chat-tool-header"><span class="chat-tool-icon">⚙️</span> <strong>' + esc(name) + '</strong></div>';
  var inputEl = document.createElement('div');
  inputEl.className = 'chat-tool-input';
  el.appendChild(inputEl);
  var resultEl = document.createElement('div');
  resultEl.className = 'chat-tool-result-inline';
  el.appendChild(resultEl);
  chatMessages.appendChild(el);
  chatCurrentToolBlock = el;
  chatCurrentToolInput = '';
  chatScrollBottom();
  return el;
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
  chatMessages.appendChild(chatAgentTracker);
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
  bubble.innerHTML = role === 'user' ? esc(text) : chatRenderMd(text);

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

  // React button for assistant bubbles
  if (role === 'assistant' && meta && meta.id) {
    const reactBtn = document.createElement('button');
    reactBtn.className = 'chat-react-btn';
    reactBtn.textContent = '😊';
    reactBtn.title = 'React';
    reactBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      chatShowReactPicker(bubble, meta.id, reactBtn);
    });
    bubble.appendChild(reactBtn);
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
  chatMessages.scrollTop = chatMessages.scrollHeight;
}

function chatSetStatus(html) {
  chatStatus.innerHTML = html;
}

function chatClearStatus() {
  chatStatus.innerHTML = '';
}

function chatRenderMd(text) {
  if (!text) return '';
  let html = esc(text);
  // Code blocks
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (_, lang, code) =>
    '<pre><code>' + code + '</code></pre>'
  );
  // Inline code
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');
  // Bold
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  // Italic
  html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');
  // Links
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
  // Line breaks → paragraphs
  html = html.replace(/\n\n+/g, '</p><p>');
  html = '<p>' + html + '</p>';
  html = html.replace(/\n/g, '<br>');
  // Clean empty paragraphs around pre blocks
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
          if (!chatStreamingText) {
            chatStreamingText = true;
            chatClearStatus();
            // Collapse thinking block when text starts arriving.
            if (chatThinkingEl && chatThinkingEl.det.open) chatThinkingEl.det.open = false;
            // Collapse agent tracker when text starts arriving.
            if (chatAgentTracker) chatAgentTracker.classList.remove('open');
            chatAssistantBubble = document.createElement('div');
            chatAssistantBubble.className = 'chat-bubble assistant';
            chatAssistantBubble.innerHTML = '';
            var metaEl = document.createElement('div');
            metaEl.className = 'chat-bubble-meta';
            var reactionsEl = document.createElement('span');
            reactionsEl.className = 'chat-reactions';
            metaEl.appendChild(reactionsEl);
            chatAssistantBubble.appendChild(metaEl);
            chatMessages.appendChild(chatAssistantBubble);
          }
          chatFullText += (data.text || '');
          chatUpdateBubbleContent(chatAssistantBubble, chatFullText);
          chatScrollBottom();
          break;
        case 'text':
          // Final full text (fallback for non-streaming or final confirmation).
          chatFullText = data.text || chatFullText;
          if (!chatAssistantBubble) {
            chatAssistantBubble = chatAppendBubble('assistant', chatFullText, {});
          } else {
            chatUpdateBubbleContent(chatAssistantBubble, chatFullText);
          }
          chatScrollBottom();
          break;
        case 'reaction':
          chatReaction = data.emoji;
          break;
        case 'done':
          chatDoneData = data;
          chatJobId = null; // job complete
          break;
        case 'error':
          toast(data.error || 'Chat error', 'error');
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
    const reactBtn = document.createElement('button');
    reactBtn.className = 'chat-react-btn';
    reactBtn.textContent = '\u{1F60A}';
    reactBtn.title = 'React';
    const msgId = chatDoneData.msg_id;
    reactBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      chatShowReactPicker(chatAssistantBubble, msgId, reactBtn);
    });
    chatAssistantBubble.appendChild(reactBtn);
  }
}

function chatFinishSend() {
  chatFinalizeBubble();
  chatClearStatus();
  chatSending = false;
  chatSendBtn.disabled = false;
  chatInput.focus();
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
}

async function chatSend() {
  const text = chatInput.value.trim();
  if (!text || chatSending) return;

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
    chatSendBtn.disabled = true;
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
    chatSendBtn.disabled = false;
    chatScrollBottom();
    return;
  }

  // Handle other slash commands.
  if (text.startsWith('/') && !text.startsWith('//')) {
    const cmdName = text.split(' ')[0];
    const isForceCmd = CHAT_COMMANDS.some(c => c.dynamic && c.name === cmdName);
    if (!isForceCmd && CHAT_COMMANDS.some(c => c.name === cmdName)) {
      chatDismissCommands();
      chatExecCommand(cmdName);
      return;
    }
  }

  chatSending = true;
  chatSendBtn.disabled = true;
  chatInput.value = '';
  chatInput.style.height = '';

  // Reset stream state.
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

  chatAppendBubble('user', text, {});
  chatScrollBottom();
  chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Thinking...');

  try {
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ message: text }),
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
    // Connection lost — try to reconnect to background job.
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
  if (document.visibilityState === 'visible' && !chatSending) {
    chatCheckActiveJob();
  }
});

// --- Chat Commands ---
const CHAT_COMMANDS = [
  { name: '/clear', description: 'Clear chat and start fresh', icon: 'trash-2' },
  { name: '/new', description: 'Start a new conversation', icon: 'refresh-cw' },
  { name: '/stop', description: 'Cancel the running request', icon: 'square' },
  { name: '/start', description: 'Re-run onboarding', icon: 'play' },
  { name: '/restart', description: 'Restart ALF daemon', icon: 'power' },
  { name: '/bash', description: 'Execute a bash command', icon: 'terminal', dynamic: true },
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
      chatAppendBubble('assistant', 'Available commands:\n' + CHAT_COMMANDS.map(c => '**' + c.name + '** — ' + c.description).join('\n'), { tier: 'system' });
      chatScrollBottom();
      break;
    case '/clear':
      fetch('/api/chat', { method: 'DELETE', credentials: 'same-origin' })
        .then(r => r.json())
        .then(r => {
          if (r.ok) {
            chatMessages.innerHTML = '';
          } else {
            chatAppendBubble('assistant', 'Failed to clear chat.', { tier: 'system' });
            chatScrollBottom();
          }
        })
        .catch(() => { chatAppendBubble('assistant', 'Failed to clear chat.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/new':
      fetch('/api/chat', { method: 'DELETE', credentials: 'same-origin' })
        .then(r => r.json())
        .then(r => {
          chatAppendBubble('assistant', r.ok ? 'New session started.' : 'Failed to start new session.', { tier: 'system' });
          chatScrollBottom();
        })
        .catch(() => { chatAppendBubble('assistant', 'Failed to start new session.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/start':
      fetch('/api/chat?onboard=1', { method: 'DELETE', credentials: 'same-origin' })
        .then(r => r.json())
        .then(r => {
          if (r.ok) {
            // Auto-trigger onboarding conversation.
            chatInput.value = 'hello';
            chatSend();
          } else {
            chatAppendBubble('assistant', 'Failed.', { tier: 'system' });
            chatScrollBottom();
          }
        })
        .catch(() => { chatAppendBubble('assistant', 'Failed.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/stop':
      fetch('/api/chat/job', { method: 'DELETE', credentials: 'same-origin' })
        .then(r => r.json())
        .then(() => {
          chatAppendBubble('assistant', 'Request cancelled.', { tier: 'system' });
          chatScrollBottom();
          if (chatSending) chatFinishSend();
        })
        .catch(() => { chatAppendBubble('assistant', 'Failed to cancel.', { tier: 'system' }); chatScrollBottom(); });
      break;
    case '/restart':
      if (!confirm('Restart ALF daemon?')) return;
      fetch('/api/restart', { method: 'POST', credentials: 'same-origin' })
        .then(() => { chatAppendBubble('assistant', 'Restart signal sent.', { tier: 'system' }); chatScrollBottom(); })
        .catch(() => { chatAppendBubble('assistant', 'Restart failed.', { tier: 'system' }); chatScrollBottom(); });
      break;
  }
}

function chatAppendSystemMessage(text) {
  const el = document.createElement('div');
  el.className = 'chat-system-msg';
  el.textContent = text;
  chatMessages.appendChild(el);
}

chatSendBtn.addEventListener('click', chatSend);
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
      // Dynamic commands (force tiers) are sent as regular messages — backend handles them.
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
  return api('/api/apps/').then(r => {
    const section = document.getElementById('navAppsSection');
    const items = r.items || [];

    section.innerHTML = '';

    items.forEach(app => {
      const a = document.createElement('a');
      a.className = 'nav-item';
      a.dataset.view = 'page:' + app.name;
      const icon = app.icon || 'app-window';
      const label = app.display_name || capitalizeName(app.name);
      a.innerHTML = '<i data-lucide="' + esc(icon) + '"></i> ' + esc(label);
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
  document.getElementById('docsSearchInput').addEventListener('input', (e) => {
    docsActiveFilter.text = e.target.value.trim().toLowerCase();
    docsUpdateList();
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
  if (docsActiveFilter.text) {
    const q = docsActiveFilter.text;
    filtered = filtered.filter(d =>
      d.title.toLowerCase().includes(q) ||
      (d.summary || '').toLowerCase().includes(q) ||
      (d.tags || []).some(t => t.toLowerCase().includes(q))
    );
  }

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
    const rendered = DOMPurify.sanitize(marked.parse(doc.content, { breaks: false, gfm: true }));

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
    content.innerHTML = html;

    document.getElementById('docsBackBtn').addEventListener('click', () => navigateTo('docs'));
    // Handle internal doc links (docs:id)
    content.querySelectorAll('.docs-article a').forEach(a => {
      const href = a.getAttribute('href');
      if (href && href.startsWith('docs:')) {
        a.addEventListener('click', (e) => {
          e.preventDefault();
          navigateTo(href);
        });
      }
    });
    // Smooth scroll for TOC links
    content.querySelectorAll('.docs-toc-item').forEach(a => {
      a.addEventListener('click', (e) => {
        e.preventDefault();
        const target = document.querySelector(a.getAttribute('href'));
        if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
      });
    });
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
let schedAllCollapsed = false;

const OUTPUTS = ['telegram', 'file', 'both', 'silent'];

function schedulesInit() {
  if (!schedulesInitialized) {
    schedulesInitialized = true;
    document.getElementById('schedulesAddBtn').addEventListener('click', () => schedulesShowModal(null));
    document.getElementById('schedCollapseAllBtn').addEventListener('click', () => {
      schedAllCollapsed = !schedAllCollapsed;
      if (schedAllCollapsed) {
        (schedulesVisible || []).forEach(j => schedCollapsedSet.add(j.id));
      } else {
        schedCollapsedSet.clear();
      }
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

  // Filter out internal system jobs — only show user/Alf-created ones.
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
  } else if (schedulesFilter === 'obsolete') {
    filtered = filtered.filter(j => j.auto_delete && (!j.next_run || new Date(j.next_run) < now));
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
    }

    const canEdit = !j.managed;
    const actions = canEdit
      ? '<button class="btn-sm sched-edit-btn" data-idx="' + i + '">Edit</button>' +
        '<button class="btn-sm btn-danger sched-delete-btn" data-idx="' + i + '">Delete</button>'
      : '<button class="btn-sm sched-toggle-btn" data-idx="' + i + '">' + (j.enabled ? 'Disable' : 'Enable') + '</button>';

    const isCollapsed = schedCollapsedSet.has(j.id);
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
  list.querySelectorAll('.sched-toggle-btn').forEach(btn => {
    btn.addEventListener('click', e => {
      e.stopPropagation();
      const j = schedulesVisible[+btn.dataset.idx];
      api('/api/schedules', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: j.id, fields: { enabled: j.enabled ? 'false' : 'true' } }),
      }).then(() => { toast(j.enabled ? 'Job disabled' : 'Job enabled'); schedulesLoad(); })
        .catch(err => toast('Toggle failed: ' + err.message, 'error'));
    });
  });
  // Collapse/expand individual cards.
  list.querySelectorAll('.sched-collapse-chevron').forEach(chevron => {
    chevron.addEventListener('click', e => {
      e.stopPropagation();
      const card = chevron.closest('.tier-card');
      const id = card.dataset.schedId;
      if (schedCollapsedSet.has(id)) {
        schedCollapsedSet.delete(id);
        schedAllCollapsed = false;
      } else {
        schedCollapsedSet.add(id);
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
        '<div class="form-row"><label>Schedule</label><input type="text" id="sjSchedule" value="' + esc(j.schedule) + '" placeholder="0 30 9 * * * (cron with seconds)"></div>' +
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
let tasksAutoTimer = null;

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
      opt.textContent = t.name + (t.description ? ' — ' + t.description : '');
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
    // Tab bar switching.
    document.querySelectorAll('.tasks-tab').forEach(tab => {
      tab.addEventListener('click', () => {
        document.querySelectorAll('.tasks-tab').forEach(t => t.classList.remove('active'));
        tab.classList.add('active');
        const target = tab.dataset.tab;
        document.getElementById('tasksRunsPane').style.display = target === 'tasks-runs' ? '' : 'none';
        document.getElementById('tasksTeamsPane').style.display = target === 'tasks-teams' ? '' : 'none';
        if (target === 'tasks-teams') teamsFetch();
      });
    });
    teamsInitEditor();
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
      launchBtn.disabled = true;
      launchBtn.innerHTML = '<span class="dot-pulse"><span></span><span></span><span></span></span> Launching...';
      try {
        const res = await fetch('/api/tasks', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          credentials: 'same-origin',
          body: JSON.stringify({ message: message }),
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
    tasksRender(data.running || [], data.completed || []);
  } catch (e) {
    document.getElementById('tasksList').innerHTML = '<div class="task-empty">Failed to load tasks</div>';
  }
}

// Track which task cards and agent steps are expanded so refresh doesn't collapse them.
let tasksExpandedSet = new Set();
let tasksStepExpandedSet = new Set();
// Track whether completed section is collapsed (default: collapsed).
let tasksCompletedVisible = false;

function tasksRender(running, completed) {
  const container = document.getElementById('tasksList');

  if (running.length === 0 && completed.length === 0) {
    container.innerHTML = '<div class="task-empty"><i data-lucide="bot" style="width:40px;height:40px;opacity:0.3;margin-bottom:8px"></i><br>No agent tasks yet.<br><span style="font-size:0.8rem;opacity:0.7">Tasks appear here when you use the agent tier.</span></div>';
    return;
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
    btn.onclick = (e) => { e.stopPropagation(); tasksRelaunch(btn.dataset.prompt); };
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

  lucide.createIcons({ attrs: { class: ['lucide'] }, nameAttr: 'data-lucide' });
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
  const statusClass = isRunning ? 'running' : (task.status === 'completed' ? 'completed' : (task.status === 'timeout' ? 'timeout' : (task.status === 'interrupted' ? 'interrupted' : 'failed')));
  const statusLabel = isRunning ? 'running' : task.status;
  const cost = task.total_cost_usd ? '$' + task.total_cost_usd.toFixed(4) : '--';
  const promptPreview = taskEscapeHtml(task.prompt || 'No prompt').substring(0, 200);
  const agentCount = (task.agent_calls && task.agent_calls.length) || 0;
  const shortId = task.id ? task.id.substring(0, 8) : '--';

  // Full request section.
  const fullPrompt = task.prompt ? '<div class="task-section"><div class="task-section-title"><i data-lucide="message-square" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Request</div><div class="task-section-body">' + taskEscapeHtml(task.prompt) + '</div></div>' : '';

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
      const callTask = call.task ? '<div class="task-step-task">' + taskEscapeHtml(call.task).substring(0, 300) + '</div>' : '';
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
    ? '<button class="btn-sm task-relaunch-btn" data-prompt="' + taskEscapeHtml(task.prompt).replace(/"/g, '&quot;') + '"><i data-lucide="rotate-cw" style="width:12px;height:12px;vertical-align:middle;margin-right:2px"></i>Relaunch</button>'
    : '';

  return '<div class="task-card ' + statusClass + '" data-task-id="' + taskEscapeHtml(task.id) + '">' +
    '<div class="task-card-header">' +
      '<div class="task-card-title">' + statusDot + '<span class="task-id">#' + shortId + '</span><strong>' + promptPreview + '</strong></div>' +
      '<span class="task-status-badge ' + statusClass + '">' + statusLabel + '</span>' +
      '<div class="task-card-actions">' +
        relaunchBtn +
        cancelBtn +
        '<span class="task-chevron"><i data-lucide="chevron-right"></i></span>' +
      '</div>' +
    '</div>' +
    '<div class="task-card-details">' +
      '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="clock" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Elapsed</span><span class="task-detail-value">' + elapsed + '</span></div>' +
      '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="coins" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Cost</span><span class="task-detail-value">' + cost + '</span></div>' +
      '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="repeat" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Iterations</span><span class="task-detail-value">' + (task.iterations || 0) + '</span></div>' +
      (agentCount > 0 ? '<div class="task-detail-row"><span class="task-detail-label"><i data-lucide="users" style="width:12px;height:12px;vertical-align:middle;margin-right:4px"></i>Agents</span><span class="task-detail-value">' + agentCount + '</span></div>' : '') +
    '</div>' +
    fullPrompt +
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

async function tasksRelaunch(prompt) {
  try {
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ message: prompt }),
    });
    if (res.ok) {
      toast('Task relaunched');
      setTimeout(() => tasksFetch(), 1000);
    } else {
      toast('Relaunch failed', 'error');
    }
  } catch (e) {
    toast('Relaunch failed: ' + e.message, 'error');
  }
}

function taskEscapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

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
      '<span class="team-agent-badge">' + taskEscapeHtml(a.name) + ' (' + taskEscapeHtml(a.model || '?') + ')</span>'
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
let logsAutoTimer = null;
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
    _logsAllLines = data.lines || [];
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

const AVAILABLE_TOOLS = [
  { name: 'Read', desc: 'Read files (code, config, logs, images, PDF)' },
  { name: 'Write', desc: 'Create or overwrite files' },
  { name: 'Edit', desc: 'Modify existing files (text replacement)' },
  { name: 'Bash', desc: 'Execute shell commands' },
  { name: 'Glob', desc: 'Search files by pattern (e.g. **/*.go)' },
  { name: 'Grep', desc: 'Search file contents with regex' },
  { name: 'WebSearch', desc: 'Search the web for information' },
  { name: 'WebFetch', desc: 'Fetch content from a URL' },
  { name: 'NotebookEdit', desc: 'Edit Jupyter notebooks' },
  { name: 'Agent', desc: 'Launch a sub-agent for complex tasks' },
];

const MODELS = ['haiku', 'sonnet', 'opus'];
const EFFORTS = ['low', 'medium', 'high'];
const BACKENDS = ['', 'cli', 'openrouter'];

let tiersCache = null;
let tiersInitialized = false;

function tiersInit() {
  if (!tiersInitialized) {
    tiersInitialized = true;
    document.getElementById('tiersAddBtn').addEventListener('click', () => tiersShowModal(null));
  }
  tiersLoad();
}

function tiersLoad() {
  api('/api/tiers').then(data => {
    tiersCache = data;
    tiersRender();
  }).catch(() => toast('Failed to load tiers', 'error'));
}

function tiersRender() {
  if (!tiersCache) return;
  const list = document.getElementById('tiersList');
  const cfg = document.getElementById('tiersRouterConfig');

  // Router config summary
  const routerBackendLabel = tiersCache.router_backend === 'openrouter' ? 'openrouter' : 'cli';
  cfg.innerHTML = '<div class="tiers-router-card">' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Router backend</span><span class="tiers-router-value">' + esc(routerBackendLabel) + '</span></div>' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Router model</span><span class="tiers-router-value">' + esc(tiersCache.router_model || 'haiku') + '</span></div>' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Default fallback</span><span class="tiers-router-value">' + esc(tiersCache.default_fallback || 'haiku') + '</span></div>' +
    '<div class="tiers-router-row"><button class="btn-sm" id="tiersEditRouterBtn">Edit router settings</button></div>' +
    '</div>';
  document.getElementById('tiersEditRouterBtn').addEventListener('click', () => tiersShowRouterModal());

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
    if (t.backend === 'openrouter') badges.push('<span class="tier-badge" style="background:rgba(139,92,246,0.15);color:#a78bfa">openrouter</span>');
    const tools = (t.tools || []).join(', ') || (t.write_capable ? 'all (write-capable)' : 'none');
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
        '<div class="tier-detail-row"><span class="tier-detail-label">Label</span><span class="tier-detail-value">' + esc(t.router_label || t.description || '—') + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Tools</span><span class="tier-detail-value">' + esc(tools) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Effort</span><span class="tier-detail-value">' + esc(t.effort || '—') + '</span></div>' +
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

  const toolChecks = AVAILABLE_TOOLS.map(tool => {
    const checked = (t.tools || []).includes(tool.name) ? ' checked' : '';
    return '<label class="tier-tool-check"><input type="checkbox" value="' + tool.name + '"' + checked + '> <strong>' + tool.name + '</strong> <span class="tier-tool-desc">— ' + esc(tool.desc) + '</span></label>';
  }).join('');

  const modelOpts = MODELS.map(m => '<option value="' + m + '"' + (t.model === m ? ' selected' : '') + '>' + m + '</option>').join('');
  const effortOpts = ['', ...EFFORTS].map(e => '<option value="' + e + '"' + (t.effort === e ? ' selected' : '') + '>' + (e || '—') + '</option>').join('');
  const backendOpts = BACKENDS.map(b => '<option value="' + b + '"' + ((t.backend || '') === b ? ' selected' : '') + '>' + (b || 'cli (default)') + '</option>').join('');
  const isOR = t.backend === 'openrouter';

  const html = '<div class="modal-backdrop" id="tierModal">' +
    '<div class="modal tier-modal">' +
      '<h3>' + (isEdit ? 'Edit Tier' : 'Add Tier') + '</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Name</label><input type="text" id="tfName" value="' + esc(t.name) + '"' + (isEdit ? ' readonly style="opacity:0.6"' : '') + '></div>' +
        '<div class="form-row"><label>Backend</label><select id="tfBackend">' + backendOpts + '</select></div>' +
        '<div class="form-row" id="tfModelRow"><label>Model</label>' + (isOR ? '<input type="text" id="tfModel" value="' + esc(t.model) + '" placeholder="e.g. anthropic/claude-haiku-4-5">' : '<select id="tfModel">' + modelOpts + '</select>') + '</div>' +
        '<div class="form-row"><label>Priority</label><input type="number" id="tfPriority" value="' + t.priority + '" min="0" max="99"></div>' +
        '<div class="form-row"><label>Effort</label><select id="tfEffort">' + effortOpts + '</select></div>' +
        '<div class="form-row"><label>Router label</label><textarea id="tfLabel" class="input tier-label-textarea" rows="2" placeholder="Description for the router">' + esc(t.router_label || '') + '</textarea></div>' +
        '<div class="form-row"><label>Description</label><input type="text" id="tfDesc" value="' + esc(t.description || '') + '" placeholder="Optional description"></div>' +
        '<div class="form-row"><label>Max turns</label><input type="number" id="tfMaxTurns" value="' + (t.max_turns || 0) + '" min="0"></div>' +
        '<div class="form-row"><label>Max iterations</label><input type="number" id="tfMaxIter" value="' + (t.max_iterations || 0) + '" min="0"></div>' +
        '<div class="form-row"><label>Timeout (min)</label><input type="number" id="tfTimeout" value="' + (t.timeout_minutes || 0) + '" min="0"></div>' +
        '<div class="tier-flags">' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfEnabled"' + (t.enabled ? ' checked' : '') + '> Enabled</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfRoutable"' + (t.routable ? ' checked' : '') + '> Routable</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfWriteCapable"' + (t.write_capable ? ' checked' : '') + '> Write capable</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfForceCmd"' + (t.force_command ? ' checked' : '') + '> Force command</label>' +
        '</div>' +
        '<div class="tier-tools-section">' +
          '<div class="tier-tools-header">Tools <span class="tier-tools-hint">(only for read-only tiers — write-capable tiers get all tools)</span></div>' +
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

  // Toggle tools visibility based on write_capable
  const wcCheck = document.getElementById('tfWriteCapable');
  const toolsSection = document.querySelector('.tier-tools-section');
  function toggleTools() { toolsSection.style.opacity = wcCheck.checked ? '0.4' : '1'; }
  toggleTools();
  wcCheck.addEventListener('change', toggleTools);

  // Swap model select/input when backend changes
  document.getElementById('tfBackend').addEventListener('change', function() {
    const row = document.getElementById('tfModelRow');
    const curVal = document.getElementById('tfModel').value;
    if (this.value === 'openrouter') {
      row.innerHTML = '<label>Model</label><input type="text" id="tfModel" value="' + esc(curVal) + '" placeholder="e.g. anthropic/claude-haiku-4-5">';
    } else {
      const opts = MODELS.map(m => '<option value="' + m + '"' + (curVal === m ? ' selected' : '') + '>' + m + '</option>').join('');
      row.innerHTML = '<label>Model</label><select id="tfModel">' + opts + '</select>';
    }
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
      tools: [],
    };
    if (!newTier.write_capable) {
      document.querySelectorAll('#tfTools input:checked').forEach(cb => newTier.tools.push(cb.value));
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
  const isOR = c.router_backend === 'openrouter';
  const modelOpts = MODELS.map(m => '<option value="' + m + '"' + (c.router_model === m ? ' selected' : '') + '>' + m + '</option>').join('');
  const fbOpts = (c.tiers || []).map(t => '<option value="' + t.name + '"' + (c.default_fallback === t.name ? ' selected' : '') + '>' + t.name + '</option>').join('');
  const rbOpts = BACKENDS.map(b => '<option value="' + b + '"' + ((c.router_backend || '') === b ? ' selected' : '') + '>' + (b || 'cli (default)') + '</option>').join('');

  const html = '<div class="modal-backdrop" id="tierRouterModal">' +
    '<div class="modal tier-modal">' +
      '<h3>Router Settings</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Router backend</label><select id="trBackend">' + rbOpts + '</select></div>' +
        '<div class="form-row" id="trModelRow"><label>Router model</label>' + (isOR ? '<input type="text" id="trModel" value="' + esc(c.router_model || '') + '" placeholder="e.g. anthropic/claude-haiku-4-5">' : '<select id="trModel">' + modelOpts + '</select>') + '</div>' +
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

  // Swap model select/input when backend changes
  document.getElementById('trBackend').addEventListener('change', function() {
    const row = document.getElementById('trModelRow');
    const curVal = document.getElementById('trModel').value;
    if (this.value === 'openrouter') {
      row.innerHTML = '<label>Router model</label><input type="text" id="trModel" value="' + esc(curVal) + '" placeholder="e.g. anthropic/claude-haiku-4-5">';
    } else {
      const opts = MODELS.map(m => '<option value="' + m + '"' + (curVal === m ? ' selected' : '') + '>' + m + '</option>').join('');
      row.innerHTML = '<label>Router model</label><select id="trModel">' + opts + '</select>';
    }
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

function tiersSave() {
  api('/api/tiers', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(tiersCache),
  }).then(() => {
    toast('Tiers saved');
    tiersRender();
  }).catch(err => {
    toast('Save failed: ' + (err.error || err.message || 'unknown'), 'error');
    tiersLoad(); // reload from server
  });
}

// --- Init ---
loadStatus();
loadTeachTiers();
loadApps().then(() => {
  const saved = localStorage.getItem('alf-view');
  navigateTo(saved || 'chat');
});
wsInit();
setInterval(loadStatus, 30000);
setInterval(loadApps, 30000);

// --- Firewall ---
let fwInitialized = false;
let fwAutoTimer = null;
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
const termThemes = {
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
};

let termInstance = null;
let termWS = null;
let termFitAddon = null;
let termResizeObserver = null;

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
  else sel.value = dark ? 'Catppuccin Mocha' : 'Catppuccin Latte';

  sel.addEventListener('change', () => {
    localStorage.setItem('alf-term-theme', sel.value);
    if (termInstance) {
      termInstance.options.theme = termThemes[sel.value];
    }
  });
})();

function termGetTheme() {
  const sel = document.getElementById('termThemeSelect');
  return termThemes[sel.value] || termThemes[dark ? 'Catppuccin Mocha' : 'Catppuccin Latte'];
}

function terminalInit() {
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

    ws.onmessage = (ev) => {
      term.write(new Uint8Array(ev.data));
    };

    ws.onclose = () => {
      term.write('\r\n\x1b[90m[session ended — click New Session to reconnect]\x1b[0m\r\n');
    };

    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(data);
      }
    });

    let resizeTimeout = null;
    function onResize() {
      clearTimeout(resizeTimeout);
      resizeTimeout = setTimeout(() => {
        fitAddon.fit();
        sendSize();
      }, 100);
    }

    window.addEventListener('resize', onResize);
    termResizeObserver = new ResizeObserver(onResize);
    termResizeObserver.observe(container);

    term.focus();
  }, 50);
}

document.getElementById('termNewBtn').addEventListener('click', terminalStart);

// --- Terminal mobile support ---
const isTouchDevice = ('ontouchstart' in window) || navigator.maxTouchPoints > 0;
if (isTouchDevice) {
  document.getElementById('termInputBar').style.display = 'flex';
  document.querySelectorAll('.term-mobile-btn').forEach(b => b.style.display = '');
}

// Paste button — read clipboard and send to terminal.
document.getElementById('termPasteBtn').addEventListener('click', async () => {
  if (!termInstance || !termWS || termWS.readyState !== WebSocket.OPEN) return;
  try {
    const text = await navigator.clipboard.readText();
    if (text) termWS.send(text);
    termInstance.focus();
  } catch { /* clipboard permission denied */ }
});

// Copy button — copy xterm selection to clipboard.
document.getElementById('termCopyBtn').addEventListener('click', async () => {
  if (!termInstance) return;
  const sel = termInstance.getSelection();
  if (sel) {
    try { await navigator.clipboard.writeText(sel); } catch {}
  }
});

// Mobile input bar — type/paste text and send with Enter or button.
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
            if (text) termWS.send(text);
          } catch {}
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

let vaultInited = false;

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
  document.getElementById('vaultCreateTokenBtn').addEventListener('click', vaultCreateToken);
  document.getElementById('vaultFileInput').addEventListener('change', vaultUploadFile);
  document.getElementById('vaultOAuth2TabBrowser').addEventListener('click', () => vaultOAuth2SetMode('browser'));
  document.getElementById('vaultOAuth2TabManual').addEventListener('click', () => vaultOAuth2SetMode('manual'));
  document.getElementById('vaultOAuth2AuthorizeBtn').addEventListener('click', vaultOAuth2StartFlow);

  vaultRefresh();
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

    // Hide everything first.
    setupCard.style.display = 'none';
    unlockCard.style.display = 'none';
    lockBtn.style.display = 'none';
    servicesCard.style.display = 'none';
    filesCard.style.display = 'none';
    tokensCard.style.display = 'none';

    if (!data || !data.available) {
      dot.className = 'vault-status-indicator vault-status-off';
      text.textContent = 'Vault not available (binary missing)';
      return;
    }

    if (data.status === 'unlocked') {
      dot.className = 'vault-status-indicator vault-status-on';
      text.textContent = 'Unlocked';
      lockBtn.style.display = '';
      servicesCard.style.display = '';
      filesCard.style.display = '';
      tokensCard.style.display = '';
      vaultLoadServices();
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
    list.innerHTML = services.map(s => `
      <div class="vault-item">
        <div class="vault-item-info">
          <span class="vault-item-name">${esc(s.name)}</span>
          <span class="vault-item-detail">${esc(s.base_url)} &middot; ${esc(s.auth_type)}${s.tls_skip_verify ? ' &middot; TLS skip' : ''}</span>
        </div>
        <div class="vault-item-actions">
          <button class="btn btn-icon vault-edit-btn" data-name="${esc(s.name)}" title="Edit"><i data-lucide="pencil"></i></button>
          <button class="btn btn-icon vault-test-btn" data-name="${esc(s.name)}" title="Test"><i data-lucide="zap"></i></button>
          <button class="btn btn-icon vault-del-btn" data-name="${esc(s.name)}" title="Delete"><i data-lucide="trash-2"></i></button>
        </div>
      </div>
    `).join('');
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
  document.getElementById('vaultSvcOAuthScopes').value = '';
  document.getElementById('vaultSvcSAFileRef').value = '';
  document.getElementById('vaultSvcSAScopes').value = '';
  document.getElementById('vaultSvcSATokenUrl').value = '';
  document.getElementById('vaultSvcTLSSkip').checked = edit ? !!edit.tls_skip_verify : false;
  if (edit) {
    document.getElementById('vaultSvcToken').placeholder = '(unchanged — leave empty to keep)';
    document.getElementById('vaultSvcHeaderValue').placeholder = '(unchanged — leave empty to keep)';
    document.getElementById('vaultSvcPassword').placeholder = '(unchanged — leave empty to keep)';
    document.getElementById('vaultSvcOAuthClientSecret').placeholder = '(unchanged — leave empty to keep)';
    document.getElementById('vaultSvcOAuthRefreshToken').placeholder = '(unchanged — leave empty to keep)';
  } else {
    document.getElementById('vaultSvcToken').placeholder = 'Bearer token';
    document.getElementById('vaultSvcHeaderValue').placeholder = 'Value';
    document.getElementById('vaultSvcPassword').placeholder = 'Password';
    document.getElementById('vaultSvcOAuthClientSecret').placeholder = 'Client Secret';
    document.getElementById('vaultSvcOAuthRefreshToken').placeholder = 'Refresh token';
  }
  vaultToggleAuthFields();
  document.getElementById('vaultServiceModal').style.display = '';
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
  if (authType === 'bearer') {
    payload.auth.token = document.getElementById('vaultSvcToken').value;
  } else if (authType === 'header') {
    payload.auth.header_name = document.getElementById('vaultSvcHeaderName').value;
    payload.auth.header_value = document.getElementById('vaultSvcHeaderValue').value;
  } else if (authType === 'basic') {
    payload.auth.username = document.getElementById('vaultSvcUsername').value;
    payload.auth.password = document.getElementById('vaultSvcPassword').value;
  } else if (authType === 'oauth2_client') {
    const oauth2Mode = document.querySelector('.oauth2-tab.active')?.dataset.mode || 'manual';
    if (oauth2Mode === 'browser') {
      // Browser flow is handled by vaultOAuth2StartFlow — nothing to save here.
      alert('Use the "Authorize in Browser" button for browser flow');
      return;
    }
    // Manual mode — only send non-empty fields (edit leaves secrets empty to keep existing).
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

async function vaultCreateToken() {
  const scope = prompt('Key scope (proxy or admin):', 'proxy');
  if (!scope) return;
  try {
    const result = await api('/api/vault/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ scope })
    });
    if (result.id) {
      alert('Key created:\n' + result.id + '\n\nSave this — it won\'t be shown again.');
    }
    vaultLoadTokens();
  } catch (err) {
    const msg = err?.error || err?.message || 'unknown error';
    alert('Create key failed: ' + msg);
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
    list.innerHTML = files.map(f => `
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

async function vaultUploadFile() {
  const input = document.getElementById('vaultFileInput');
  const file = input.files[0];
  if (!file) return;
  const name = prompt('File name in vault:', file.name);
  if (!name) { input.value = ''; return; }

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
    alert('Upload failed: ' + (err?.error || err?.message || 'unknown error'));
  }
  input.value = '';
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

