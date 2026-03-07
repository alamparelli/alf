// Auth: session cookie set by magic link flow (/auth endpoint).
// No tokens in URL, meta tags, or sessionStorage.
sessionStorage.removeItem('cc_token'); // cleanup legacy token

function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
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
const themeBtn = document.getElementById('themeToggle');
let dark = localStorage.getItem('alf-theme') !== 'light';
document.body.classList.toggle('light', !dark);
themeBtn.textContent = dark ? 'Light' : 'Dark';
themeBtn.onclick = () => {
  dark = !dark;
  document.body.classList.toggle('light', !dark);
  themeBtn.textContent = dark ? 'Light' : 'Dark';
  localStorage.setItem('alf-theme', dark ? 'dark' : 'light');
  syncIframeTheme();
};

function syncIframeTheme() {
  const frame = document.getElementById('pageFrame');
  try {
    const doc = frame.contentDocument;
    if (!doc || !doc.documentElement) return;

    // Toggle light class on the iframe's html element.
    doc.documentElement.classList.toggle('light', !dark);

    // Inject theme.css if not already present.
    if (!doc.getElementById('alf-theme')) {
      const link = doc.createElement('link');
      link.id = 'alf-theme';
      link.rel = 'stylesheet';
      link.href = '/static/theme.css';
      doc.head.appendChild(link);
    }
  } catch (_) { /* cross-origin or not loaded */ }
}

// Sync theme when iframe loads a new page.
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

  // Update active nav item — docs:id should highlight the docs nav item
  const navView = view.startsWith('docs:') ? 'docs' : view;
  logsStopAutoRefresh();
  tasksStopAutoRefresh();
  document.querySelectorAll('#sidebarNav .nav-item').forEach(el => {
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
    pageFrame.src = '/pages/' + encodeURIComponent(name);
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
  }

  localStorage.setItem('alf-view', view);

  // Close sidebar on mobile
  sidebar.classList.remove('open');
  sidebarOverlay.classList.remove('open');
}

// Bind Home + Chat + Docs + Logs nav
document.querySelector('#sidebarNav .nav-item[data-view="home"]').addEventListener('click', () => navigateTo('home'));
document.querySelector('#sidebarNav .nav-item[data-view="chat"]').addEventListener('click', () => navigateTo('chat'));
document.querySelector('#sidebarNav .nav-item[data-view="schedules"]').addEventListener('click', () => navigateTo('schedules'));
document.querySelector('#sidebarNav .nav-item[data-view="tasks"]').addEventListener('click', () => navigateTo('tasks'));
document.querySelector('#sidebarNav .nav-item[data-view="logs"]').addEventListener('click', () => navigateTo('logs'));
document.querySelector('#sidebarNav .nav-item[data-view="docs"]').addEventListener('click', () => navigateTo('docs'));
document.querySelector('#sidebarNav .nav-item[data-view="tiers"]').addEventListener('click', () => navigateTo('tiers'));

// --- Status ---
function loadStatus() {
  api('/api/status').catch(() => {});
}


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
  const dirs = ['skills.d', 'tools', 'context.d', 'config.d', 'memory.d', 'pages.d'];
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
    .catch(() => {});
}

const QUICK_REACTIONS = ['👍', '❤', '🔥', '😁', '🤔', '👎'];

function chatAppendStep(type, label) {
  const el = document.createElement('div');
  el.className = 'chat-step ' + type;
  if (type === 'thinking') {
    el.innerHTML = '<span class="chat-step-icon">🧠</span> <span class="chat-step-label">' + esc(label) + '</span>';
  } else {
    el.innerHTML = '<span class="chat-step-icon">⚙️</span> <span class="chat-step-label">' + esc(label) + '</span>';
  }
  chatMessages.appendChild(el);
  chatScrollBottom();
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

  chatAppendBubble('user', text, {});
  chatScrollBottom();
  chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Thinking...');

  let assistantBubble = null;
  let fullText = '';
  let doneData = null;
  let reaction = null;

  try {
    const res = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ message: text }),
    });

    if (res.status === 401) {
      toast('Session expired', 'error');
      chatClearStatus();
      chatSending = false;
      chatSendBtn.disabled = false;
      return;
    }

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
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

        switch (eventType) {
          case 'thinking':
            chatAppendStep('thinking', 'Thinking...');
            chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Thinking...');
            break;
          case 'tool_use':
            chatAppendStep('tool_use', data.name || 'tool');
            chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Using ' + esc(data.name || 'tool') + '...');
            break;
          case 'text':
            fullText = data.text || '';
            assistantBubble = chatAppendBubble('assistant', fullText, {});
            chatScrollBottom();
            break;
          case 'reaction':
            reaction = data.emoji;
            break;
          case 'done':
            doneData = data;
            break;
          case 'error':
            toast(data.error || 'Chat error', 'error');
            break;
        }
      }
    }

    // Update assistant bubble meta with done data
    if (assistantBubble && doneData) {
      assistantBubble.dataset.msgId = doneData.msg_id;
      const metaEl = assistantBubble.querySelector('.chat-bubble-meta');
      if (metaEl) {
        // Rebuild meta text (keep reactions container)
        const reactionsEl = metaEl.querySelector('.chat-reactions');
        let parts = [new Date().toLocaleTimeString()];
        if (doneData.tier) parts.push(doneData.tier);
        if (doneData.model) parts.push(doneData.model);
        metaEl.textContent = parts.join(' · ');
        // Re-add reactions container
        if (reactionsEl) {
          metaEl.appendChild(reactionsEl);
        } else {
          const newReactionsEl = document.createElement('span');
          newReactionsEl.className = 'chat-reactions';
          metaEl.appendChild(newReactionsEl);
        }
        if (reaction) {
          const rEl = metaEl.querySelector('.chat-reactions');
          const span = document.createElement('span');
          span.className = 'chat-bubble-reaction';
          span.textContent = reaction;
          if (rEl) rEl.appendChild(span);
        }
      }
      // Add react button
      const reactBtn = document.createElement('button');
      reactBtn.className = 'chat-react-btn';
      reactBtn.textContent = '😊';
      reactBtn.title = 'React';
      reactBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        chatShowReactPicker(assistantBubble, doneData.msg_id, reactBtn);
      });
      assistantBubble.appendChild(reactBtn);
    }

  } catch (e) {
    toast('Failed to send message', 'error');
  }

  chatClearStatus();
  chatSending = false;
  chatSendBtn.disabled = false;
  chatInput.focus();
}

// --- Chat Commands ---
const CHAT_COMMANDS = [
  { name: '/clear', description: 'Clear chat and start fresh', icon: 'trash-2' },
  { name: '/new', description: 'Start a new conversation', icon: 'refresh-cw' },
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
          chatAppendBubble('assistant', r.ok ? 'Onboarding active — send a message to begin.' : 'Failed.', { tier: 'system' });
          chatScrollBottom();
        })
        .catch(() => { chatAppendBubble('assistant', 'Failed.', { tier: 'system' }); chatScrollBottom(); });
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

// --- Pages (sidebar nav) ---
function capitalizeName(name) {
  return name.replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
}

function loadPages() {
  return api('/api/pages/').then(r => {
    const nav = document.getElementById('sidebarNav');
    const items = r.items || [];

    // Remove existing page nav items (keep Home)
    nav.querySelectorAll('.nav-item[data-view^="page:"]').forEach(el => el.remove());

    const spacer = document.getElementById('navSpacer');
    items.forEach(p => {
      const a = document.createElement('a');
      a.className = 'nav-item';
      a.dataset.view = 'page:' + p.name;
      a.innerHTML = '<i data-lucide="file-code"></i> ' + esc(capitalizeName(p.name));
      a.addEventListener('click', () => navigateTo(a.dataset.view));
      nav.insertBefore(a, spacer);
    });

    // Restore active state
    const activeView = document.querySelector('#sidebarNav .nav-item.active');
    if (activeView) {
      nav.querySelectorAll('.nav-item').forEach(el => {
        el.classList.toggle('active', el.dataset.view === activeView.dataset.view);
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
    const rendered = marked.parse(doc.content, { breaks: false, gfm: true });

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

const OUTPUTS = ['telegram', 'file', 'both', 'silent'];

function schedulesInit() {
  if (!schedulesInitialized) {
    schedulesInitialized = true;
    document.getElementById('schedulesAddBtn').addEventListener('click', () => schedulesShowModal(null));
  }
  schedulesLoad();
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
  schedulesVisible = (schedulesCache || []).filter(j => !j.system);
  const visible = schedulesVisible;

  if (!visible.length) {
    list.innerHTML = '<div class="task-empty"><div class="task-empty-icon">&#128197;</div>No scheduled jobs yet.<br><span style="font-size:0.8rem;opacity:0.7">Create jobs to run prompts or commands on a schedule.</span></div>';
    return;
  }

  list.innerHTML = visible.map((j, i) => {
    const statusDot = j.enabled ? '<span class="dot green"></span>' : '<span class="dot red"></span>';
    const badges = [];
    if (j.system) badges.push('<span class="tier-badge tier-badge-routable">system</span>');
    if (j.managed) badges.push('<span class="tier-badge tier-badge-instant">managed</span>');
    if (j.auto_delete) badges.push('<span class="tier-badge tier-badge-force">one-shot</span>');
    const tierLabel = j.tier === 'direct' ? 'direct (bash)' : (j.tier || 'default');
    const outputBadge = '<span class="tier-badge tier-badge-routable">' + esc(j.output || 'telegram') + '</span>';
    const nextRun = j.next_run ? schedRelTime(j.next_run) : '--';
    const lastRun = j.last_run ? schedRelTime(j.last_run) : '--';
    const prompt = j.tier === 'direct' ? (j.command || '--') : (j.prompt || '--');

    const canEdit = !j.managed;
    const actions = canEdit
      ? '<button class="btn-sm sched-edit-btn" data-idx="' + i + '">Edit</button>' +
        '<button class="btn-sm btn-danger sched-delete-btn" data-idx="' + i + '">Delete</button>'
      : '<button class="btn-sm sched-toggle-btn" data-idx="' + i + '">' + (j.enabled ? 'Disable' : 'Enable') + '</button>';

    return '<div class="tier-card" data-idx="' + i + '">' +
      '<div class="tier-card-header">' +
        '<div class="tier-card-title">' + statusDot + '<strong>' + esc(j.name) + '</strong></div>' +
        '<span class="tier-model-badge" style="color:var(--text-dim)">' + esc(j.schedule) + '</span>' +
        '<div class="tier-card-actions">' + actions + '</div>' +
      '</div>' +
      '<div class="tier-card-details">' +
        '<div class="tier-detail-row"><span class="tier-detail-label">ID</span><span class="tier-detail-value" style="font-family:monospace;font-size:0.75rem;opacity:0.7">' + esc(j.id) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Tier</span><span class="tier-detail-value">' + esc(tierLabel) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">' + (j.tier === 'direct' ? 'Command' : 'Prompt') + '</span><span class="tier-detail-value sched-prompt">' + esc(prompt.substring(0, 200)) + '</span></div>' +
        '<div class="tier-detail-row"><span class="tier-detail-label">Output</span><span class="tier-detail-value">' + outputBadge + '</span></div>' +
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
        '<div class="form-row"><label>Tier</label><input type="text" id="sjTier" value="' + esc(j.tier || '') + '" placeholder="haiku_r, sonnet_rw, direct..."></div>' +
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

function tasksRender(running, completed) {
  const container = document.getElementById('tasksList');

  if (running.length === 0 && completed.length === 0) {
    container.innerHTML = '<div class="task-empty"><div class="task-empty-icon">\u{1F916}</div>No agent tasks yet.<br><span style="font-size:0.8rem;opacity:0.7">Tasks appear here when you use the agent tier.</span></div>';
    return;
  }

  let html = '';

  if (running.length > 0) {
    html += '<h3 class="tasks-section-title">\u{26A1} Running (' + running.length + ')</h3>';
    for (const task of running) {
      html += taskCard(task, true);
    }
  }

  if (completed.length > 0) {
    html += '<h3 class="tasks-section-title">Recent</h3>';
    for (const task of completed) {
      html += taskCard(task, false);
    }
  }

  container.innerHTML = html;

  container.querySelectorAll('.task-cancel-btn').forEach(btn => {
    btn.onclick = (e) => { e.stopPropagation(); tasksCancel(btn.dataset.id); };
  });

  container.querySelectorAll('.task-card-header').forEach(header => {
    header.onclick = () => {
      const card = header.closest('.task-card');
      card.classList.toggle('expanded');
    };
  });

  lucide.createIcons({ attrs: { class: ['lucide'] }, nameAttr: 'data-lucide' });
}

function taskCard(task, isRunning) {
  const elapsed = taskElapsed(task.started_at);
  const statusClass = isRunning ? 'running' : (task.status === 'completed' ? 'completed' : (task.status === 'timeout' ? 'timeout' : 'failed'));
  const statusLabel = isRunning ? 'running' : task.status;
  const cost = task.total_cost_usd ? '$' + task.total_cost_usd.toFixed(4) : '--';
  const prompt = taskEscapeHtml(task.prompt || 'No prompt').substring(0, 200);
  const agentCount = (task.agent_calls && task.agent_calls.length) || 0;

  let agentSteps = '';
  if (agentCount > 0) {
    agentSteps = '<div class="task-steps"><div class="task-steps-title">Agent calls (' + agentCount + ')</div>';
    for (const call of task.agent_calls) {
      const agentName = call.agent.split('/').pop();
      const callStatus = call.error ? 'failed' : 'completed';
      const callIcon = call.error ? '\u2717' : '\u2713';
      const callCost = call.cost_usd ? '$' + call.cost_usd.toFixed(4) : '';
      const callResult = call.error
        ? taskEscapeHtml(call.error).substring(0, 300)
        : taskEscapeHtml(call.text || '').substring(0, 300);
      agentSteps += '<div class="task-step ' + callStatus + '">' +
        '<span class="task-step-icon">' + callIcon + '</span>' +
        '<span class="task-step-agent">' + taskEscapeHtml(agentName) + '</span>' +
        '<span class="task-step-cost">' + callCost + '</span>' +
        (callResult ? '<div class="task-step-result">' + callResult + '</div>' : '') +
        '</div>';
    }
    agentSteps += '</div>';
  }

  const statusDot = '<span class="dot ' + (isRunning ? 'blue' : statusClass === 'completed' ? 'green' : 'red') + '"></span>';
  const cancelBtn = isRunning
    ? '<button class="btn-sm btn-danger task-cancel-btn" data-id="' + taskEscapeHtml(task.id) + '">Cancel</button>'
    : '';

  return '<div class="task-card ' + statusClass + '">' +
    '<div class="task-card-header">' +
      '<div class="task-card-title">' + statusDot + '<strong>' + prompt + '</strong></div>' +
      '<span class="task-status-badge ' + statusClass + '">' + statusLabel + '</span>' +
      '<div class="task-card-actions">' +
        (agentCount > 0 ? '<span class="task-chevron"><i data-lucide="chevron-right"></i></span>' : '') +
        cancelBtn +
      '</div>' +
    '</div>' +
    '<div class="task-card-details">' +
      '<div class="task-detail-row"><span class="task-detail-label">Elapsed</span><span class="task-detail-value">' + elapsed + '</span></div>' +
      '<div class="task-detail-row"><span class="task-detail-label">Cost</span><span class="task-detail-value">' + cost + '</span></div>' +
      '<div class="task-detail-row"><span class="task-detail-label">Iterations</span><span class="task-detail-value">' + (task.iterations || 0) + '</span></div>' +
      (agentCount > 0 ? '<div class="task-detail-row"><span class="task-detail-label">Agents</span><span class="task-detail-value">' + agentCount + '</span></div>' : '') +
    '</div>' +
    agentSteps +
  '</div>';
}

function taskElapsed(startedAt) {
  if (!startedAt) return '--';
  const start = new Date(startedAt);
  const now = new Date();
  const diffMs = now - start;
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

function taskEscapeHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
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
async function logsFetch() {
  const name = document.getElementById('logSelect').value;
  const n = document.getElementById('logLines').value;
  if (!name) return;
  try {
    const res = await fetch(`/api/logs?name=${encodeURIComponent(name)}&n=${n}`);
    const data = await res.json();
    _logsAllLines = data.lines || [];
    logsApplyFilter();
  } catch {
    document.getElementById('logOutput').textContent = 'Failed to load logs.';
  }
}

function logsApplyFilter() {
  const q = document.getElementById('logSearch').value.toLowerCase();
  const out = document.getElementById('logOutput');
  const filtered = q ? _logsAllLines.filter(l => l.toLowerCase().includes(q)) : _logsAllLines;
  out.innerHTML = '';
  filtered.forEach(line => {
    const span = document.createElement('span');
    span.className = 'log-line' + logsLineClass(line);
    span.textContent = line;
    out.appendChild(span);
    out.appendChild(document.createTextNode('\n'));
  });
  out.scrollTop = out.scrollHeight;
}

function logsLineClass(line) {
  if (/\bERROR\b/i.test(line)) return ' log-error';
  if (/\bWARN(ING)?\b/i.test(line)) return ' log-warn';
  if (/\bDEBUG\b/i.test(line)) return ' log-debug';
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
  cfg.innerHTML = '<div class="tiers-router-card">' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Router model</span><span class="tiers-router-value">' + esc(tiersCache.router_model || 'haiku') + '</span></div>' +
    '<div class="tiers-router-row"><span class="tiers-router-label">Default fallback</span><span class="tiers-router-value">' + esc(tiersCache.default_fallback || 'haiku_r') + '</span></div>' +
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
    if (t.instant) badges.push('<span class="tier-badge tier-badge-instant">instant</span>');
    if (t.write_capable) badges.push('<span class="tier-badge tier-badge-write">write</span>');
    if (t.force_command) badges.push('<span class="tier-badge tier-badge-force">force</span>');
    if (t.routable) badges.push('<span class="tier-badge tier-badge-routable">routable</span>');
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
  const t = tier || { name: '', model: 'haiku', priority: 0, enabled: true, routable: true, router_label: '', effort: 'low', tools: [], write_capable: false, force_command: false, instant: false, max_turns: 0, max_iterations: 0, timeout_minutes: 0, description: '' };

  // Remove existing modal
  const old = document.getElementById('tierModal');
  if (old) old.remove();

  const toolChecks = AVAILABLE_TOOLS.map(tool => {
    const checked = (t.tools || []).includes(tool.name) ? ' checked' : '';
    return '<label class="tier-tool-check"><input type="checkbox" value="' + tool.name + '"' + checked + '> <strong>' + tool.name + '</strong> <span class="tier-tool-desc">— ' + esc(tool.desc) + '</span></label>';
  }).join('');

  const modelOpts = MODELS.map(m => '<option value="' + m + '"' + (t.model === m ? ' selected' : '') + '>' + m + '</option>').join('');
  const effortOpts = ['', ...EFFORTS].map(e => '<option value="' + e + '"' + (t.effort === e ? ' selected' : '') + '>' + (e || '—') + '</option>').join('');

  const html = '<div class="modal-backdrop" id="tierModal">' +
    '<div class="modal tier-modal">' +
      '<h3>' + (isEdit ? 'Edit Tier' : 'Add Tier') + '</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Name</label><input type="text" id="tfName" value="' + esc(t.name) + '"' + (isEdit ? ' readonly style="opacity:0.6"' : '') + '></div>' +
        '<div class="form-row"><label>Model</label><select id="tfModel">' + modelOpts + '</select></div>' +
        '<div class="form-row"><label>Priority</label><input type="number" id="tfPriority" value="' + t.priority + '" min="0" max="99"></div>' +
        '<div class="form-row"><label>Effort</label><select id="tfEffort">' + effortOpts + '</select></div>' +
        '<div class="form-row"><label>Router label</label><input type="text" id="tfLabel" value="' + esc(t.router_label || '') + '" placeholder="Description for the router"></div>' +
        '<div class="form-row"><label>Description</label><input type="text" id="tfDesc" value="' + esc(t.description || '') + '" placeholder="Optional description"></div>' +
        '<div class="form-row"><label>Max turns</label><input type="number" id="tfMaxTurns" value="' + (t.max_turns || 0) + '" min="0"></div>' +
        '<div class="form-row"><label>Max iterations</label><input type="number" id="tfMaxIter" value="' + (t.max_iterations || 0) + '" min="0"></div>' +
        '<div class="form-row"><label>Timeout (min)</label><input type="number" id="tfTimeout" value="' + (t.timeout_minutes || 0) + '" min="0"></div>' +
        '<div class="tier-flags">' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfEnabled"' + (t.enabled ? ' checked' : '') + '> Enabled</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfRoutable"' + (t.routable ? ' checked' : '') + '> Routable</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfWriteCapable"' + (t.write_capable ? ' checked' : '') + '> Write capable</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfForceCmd"' + (t.force_command ? ' checked' : '') + '> Force command</label>' +
          '<label class="tier-flag-check"><input type="checkbox" id="tfInstant"' + (t.instant ? ' checked' : '') + '> Instant</label>' +
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

  document.getElementById('tierModalCancel').addEventListener('click', () => document.getElementById('tierModal').remove());
  document.getElementById('tierModal').addEventListener('click', e => { if (e.target.id === 'tierModal') document.getElementById('tierModal').remove(); });

  document.getElementById('tierModalSave').addEventListener('click', () => {
    const newTier = {
      name: document.getElementById('tfName').value.trim(),
      model: document.getElementById('tfModel').value,
      priority: parseInt(document.getElementById('tfPriority').value, 10) || 0,
      enabled: document.getElementById('tfEnabled').checked,
      routable: document.getElementById('tfRoutable').checked,
      instant: document.getElementById('tfInstant').checked,
      router_label: document.getElementById('tfLabel').value.trim(),
      description: document.getElementById('tfDesc').value.trim(),
      max_turns: parseInt(document.getElementById('tfMaxTurns').value, 10) || 0,
      max_iterations: parseInt(document.getElementById('tfMaxIter').value, 10) || 0,
      timeout_minutes: parseInt(document.getElementById('tfTimeout').value, 10) || 0,
      effort: document.getElementById('tfEffort').value,
      write_capable: document.getElementById('tfWriteCapable').checked,
      force_command: document.getElementById('tfForceCmd').checked,
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
  const modelOpts = MODELS.map(m => '<option value="' + m + '"' + (c.router_model === m ? ' selected' : '') + '>' + m + '</option>').join('');
  const fbOpts = (c.tiers || []).map(t => '<option value="' + t.name + '"' + (c.default_fallback === t.name ? ' selected' : '') + '>' + t.name + '</option>').join('');

  const html = '<div class="modal-backdrop" id="tierRouterModal">' +
    '<div class="modal tier-modal">' +
      '<h3>Router Settings</h3>' +
      '<div class="tier-form">' +
        '<div class="form-row"><label>Router model</label><select id="trModel">' + modelOpts + '</select></div>' +
        '<div class="form-row"><label>Default fallback</label><select id="trFallback">' + fbOpts + '</select></div>' +
        '<div class="form-row"><label>Instant label</label><input type="text" id="trInstant" value="' + esc(c.router_instant_label || '') + '"></div>' +
        '<div class="form-row"><label>Distinctions</label><textarea class="json-editor" id="trDistinctions" rows="4">' + esc(c.router_distinctions || '') + '</textarea></div>' +
      '</div>' +
      '<div class="upload-actions">' +
        '<button class="btn" id="trCancel">Cancel</button>' +
        '<button class="btn btn-primary" id="trSave">Save</button>' +
      '</div>' +
    '</div>' +
  '</div>';

  document.body.insertAdjacentHTML('beforeend', html);
  document.getElementById('trCancel').addEventListener('click', () => document.getElementById('tierRouterModal').remove());
  document.getElementById('tierRouterModal').addEventListener('click', e => { if (e.target.id === 'tierRouterModal') document.getElementById('tierRouterModal').remove(); });

  document.getElementById('trSave').addEventListener('click', () => {
    tiersCache.router_model = document.getElementById('trModel').value;
    tiersCache.default_fallback = document.getElementById('trFallback').value;
    tiersCache.router_instant_label = document.getElementById('trInstant').value.trim();
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
loadPages().then(() => {
  const saved = localStorage.getItem('alf-view');
  if (saved && saved !== 'home') navigateTo(saved);
});
wsInit();
setInterval(loadStatus, 30000);
setInterval(loadPages, 30000);
