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
  const pageFrame = document.getElementById('pageFrame');

  // Update active nav item
  document.querySelectorAll('#sidebarNav .nav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.view === view);
  });

  homeView.style.display = 'none';
  chatView.style.display = 'none';
  pageFrame.style.display = 'none';

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
  }

  // Close sidebar on mobile
  sidebar.classList.remove('open');
  sidebarOverlay.classList.remove('open');
}

// Bind Home + Chat nav
document.querySelector('#sidebarNav .nav-item[data-view="home"]').addEventListener('click', () => navigateTo('home'));
document.querySelector('#sidebarNav .nav-item[data-view="chat"]').addEventListener('click', () => navigateTo('chat'));

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
            chatSetStatus('<span class="dot-pulse"><span></span><span></span><span></span></span> Thinking...');
            break;
          case 'tool_use':
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
  { name: '/new', description: 'Start a new conversation', icon: 'refresh-cw' },
  { name: '/start', description: 'Re-run onboarding', icon: 'play' },
  { name: '/restart', description: 'Restart ALF daemon', icon: 'power' },
  { name: '/help', description: 'Show available commands', icon: 'help-circle' },
];

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
        chatDismissCommands();
        chatExecCommand(cmdName);
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
      if (cmd) {
        chatDismissCommands();
        chatExecCommand(cmd.name);
        return;
      }
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
