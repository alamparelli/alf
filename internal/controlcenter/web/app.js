// Auth token management.
let TOKEN = '';
(function initAuth() {
  const params = new URLSearchParams(location.search);
  if (params.get('token')) {
    TOKEN = params.get('token');
    sessionStorage.setItem('cc_token', TOKEN);
    history.replaceState(null, '', location.pathname);
  } else {
    TOKEN = sessionStorage.getItem('cc_token') || '';
  }
  if (!TOKEN) {
    const meta = document.querySelector('meta[name="auth-token"]');
    if (meta && meta.content && meta.content !== '{{AUTH_TOKEN}}') {
      TOKEN = meta.content;
      sessionStorage.setItem('cc_token', TOKEN);
    }
  }
})();

function api(path, opts = {}) {
  const headers = { ...(opts.headers || {}) };
  if (TOKEN) {
    headers['Authorization'] = 'Bearer ' + TOKEN;
  }
  return fetch(path, { ...opts, headers, credentials: 'same-origin' }).then(r => {
    if (r.status === 401) { toast('Unauthorized — invalid token', 'error'); throw new Error('401'); }
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
let dark = true;
themeBtn.onclick = () => {
  dark = !dark;
  document.body.classList.toggle('light', !dark);
  themeBtn.textContent = dark ? 'Light' : 'Dark';
};

// --- Status ---
function loadStatus() {
  api('/api/status').then(s => {
    document.getElementById('statusText').textContent = s.status || 'unknown';
    const dot = document.getElementById('statusDot');
    dot.className = 'dot ' + (s.status === 'running' ? 'green' : 'red');
    document.getElementById('uptimeValue').textContent = s.uptime || '--';
    document.getElementById('msgCount').textContent = s.message_count || 0;
    if (s.last_message) {
      const d = new Date(s.last_message);
      document.getElementById('lastMsg').textContent = d.toLocaleTimeString();
    }
  }).catch(() => {
    document.getElementById('statusText').textContent = 'error';
    document.getElementById('statusDot').className = 'dot red';
  });
}

// --- Config (read-only) ---
function loadConfig() {
  api('/api/config').then(cfg => {
    document.getElementById('cfgLogLevel').textContent = cfg.log_level || 'info';
    const qs = cfg.quiet_hours?.start || 0;
    const qe = cfg.quiet_hours?.end || 0;
    document.getElementById('cfgQuietHours').textContent = (qs === 0 && qe === 0) ? 'Disabled' : qs + ':00 — ' + qe + ':00';
    document.getElementById('cfgSystemPrompt').textContent = cfg.system_prompt || '(default)';
  }).catch(() => {});
}

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// --- Workspace Explorer (Tree) ---
let wsOpenPath = null;
let wsTree = {};  // { [dirPath]: { loaded, expanded, entries[] } }

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

      html += '<div class="ws-node ws-node-dir' + (expanded ? ' expanded' : '') + '" data-path="' + esc(fullPath) + '" style="padding-left:' + (8 + depth * 20) + 'px">' +
        wsIcon(chevronIcon, 'ws-icon ws-icon-chevron') +
        wsIcon(folderIcon, 'ws-icon ws-icon-folder') +
        '<span class="ws-node-label">' + esc(e.name) + '</span>' +
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

function wsOpenFile(filePath) {
  wsOpenPath = filePath;
  const editor = document.getElementById('wsEditor');
  const msg = document.getElementById('wsMessage');
  const saveBtn = document.getElementById('wsSaveBtn');
  const deleteBtn = document.getElementById('wsDeleteBtn');

  deleteBtn.disabled = true;

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
      document.getElementById('wsBreadcrumb').innerHTML = wsBreadcrumbHTML('');
      if (window.lucide) lucide.createIcons();
      // Re-fetch parent dir to update tree.
      delete wsTree[parentPath];
      wsToggleDir(parentPath);
    } else toast(r.error || 'Delete failed', 'error');
  }).catch(e => toast(e.error || 'Delete failed', 'error'));
});

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

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  return (bytes / 1024).toFixed(1) + ' KB';
}


// --- Init ---
loadStatus();
loadConfig();
wsInit();
setInterval(loadStatus, 30000);
