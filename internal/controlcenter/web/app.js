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
let dark = true;
themeBtn.onclick = () => {
  dark = !dark;
  document.body.classList.toggle('light', !dark);
  themeBtn.textContent = dark ? 'Light' : 'Dark';
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
  const pageFrame = document.getElementById('pageFrame');

  // Update active nav item
  document.querySelectorAll('#sidebarNav .nav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.view === view);
  });

  if (view === 'home') {
    homeView.style.display = '';
    pageFrame.style.display = 'none';
    pageFrame.src = '';
  } else if (view.startsWith('page:')) {
    const name = view.slice(5);
    homeView.style.display = 'none';
    pageFrame.style.display = '';
    pageFrame.src = '/pages/' + encodeURIComponent(name);
  }

  // Close sidebar on mobile
  sidebar.classList.remove('open');
  sidebarOverlay.classList.remove('open');
}

// Bind Home nav
document.querySelector('#sidebarNav .nav-item[data-view="home"]').addEventListener('click', () => navigateTo('home'));

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

      const canDeleteDir = depth > 0 || !['config.d','context.d','memory.d','pages.d','skills.d','tools'].includes(e.name);
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

// --- Pages (sidebar nav) ---
function capitalizeName(name) {
  return name.replace(/-/g, ' ').replace(/\b\w/g, c => c.toUpperCase());
}

function loadPages() {
  api('/api/pages/').then(r => {
    const nav = document.getElementById('sidebarNav');
    const items = r.items || [];

    // Remove existing page nav items (keep Home)
    nav.querySelectorAll('.nav-item[data-view^="page:"]').forEach(el => el.remove());

    items.forEach(p => {
      const a = document.createElement('a');
      a.className = 'nav-item';
      a.dataset.view = 'page:' + p.name;
      a.innerHTML = '<i data-lucide="file-code"></i> ' + esc(capitalizeName(p.name));
      a.addEventListener('click', () => navigateTo(a.dataset.view));
      nav.appendChild(a);
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

// --- Init ---
loadStatus();
loadConfig();
loadTeachTiers();
loadPages();
wsInit();
setInterval(loadStatus, 30000);
setInterval(loadPages, 30000);
