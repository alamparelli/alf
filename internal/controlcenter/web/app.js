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

// --- Advanced toggle ---
const advBtn = document.getElementById('advancedToggle');
let adv = false;
advBtn.onclick = () => {
  adv = !adv;
  advBtn.classList.toggle('active', adv);
  document.querySelectorAll('.advanced').forEach(el => el.classList.toggle('visible', adv));
  if (adv) { loadRawConfig(); loadTiers(); loadLogFiles(); }
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
    document.getElementById('sysInfo').textContent = 'Version: ' + (s.version || 'unknown');
  }).catch(() => {
    document.getElementById('statusText').textContent = 'error';
    document.getElementById('statusDot').className = 'dot red';
  });
}

// --- Config (read-only) ---
function loadConfig() {
  api('/api/config').then(cfg => {
    document.getElementById('cfgModel').textContent = cfg.model || 'sonnet';
    document.getElementById('cfgLogLevel').textContent = cfg.log_level || 'info';
    const qs = cfg.quiet_hours?.start || 0;
    const qe = cfg.quiet_hours?.end || 0;
    document.getElementById('cfgQuietHours').textContent = (qs === 0 && qe === 0) ? 'Disabled' : qs + ':00 — ' + qe + ':00';
    document.getElementById('cfgSystemPrompt').textContent = cfg.system_prompt || '(default)';
  }).catch(() => {});
}

// --- Raw Config (read-only) ---
function loadRawConfig() {
  api('/api/config').then(cfg => {
    document.getElementById('rawConfigDisplay').textContent = JSON.stringify(cfg, null, 2);
  }).catch(() => {});
}

// --- Tiers (read-only) ---
function loadTiers() {
  api('/api/tiers').then(t => {
    const body = document.getElementById('tiersBody');
    body.innerHTML = '';
    (t.tiers || []).forEach(tier => {
      const tr = document.createElement('tr');
      tr.innerHTML = `
        <td>${esc(tier.name)}</td>
        <td>${esc(tier.model)}</td>
        <td>${tier.priority}</td>
        <td>${tier.enabled ? 'Yes' : 'No'}</td>
      `;
      body.appendChild(tr);
    });
  }).catch(() => {});
}

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// --- Logs ---
function loadRecentLogs() {
  api('/api/logs?name=daemon.log&n=20').then(r => {
    const box = document.getElementById('logBox');
    box.textContent = (r.lines || []).join('\n') || 'No logs available';
    box.scrollTop = box.scrollHeight;
  }).catch(() => {
    document.getElementById('logBox').textContent = 'Failed to load logs';
  });
}

function loadLogFiles() {
  api('/api/logs').then(r => {
    const sel = document.getElementById('logFileName');
    sel.innerHTML = '';
    (r.available || []).forEach(name => {
      const opt = document.createElement('option');
      opt.value = name; opt.textContent = name;
      sel.appendChild(opt);
    });
  }).catch(() => {});
}

document.getElementById('fetchLogsBtn').onclick = () => {
  const name = document.getElementById('logFileName').value;
  const n = document.getElementById('logLines').value;
  if (!name) return;
  api('/api/logs?name=' + encodeURIComponent(name) + '&n=' + n).then(r => {
    const box = document.getElementById('fullLogBox');
    box.textContent = (r.lines || []).join('\n') || 'No lines';
    box.scrollTop = box.scrollHeight;
  }).catch(() => toast('Failed to fetch logs', 'error'));
};

// --- Generic Resource CRUD ---
function resourceAPI(type_) {
  return {
    list: () => api('/api/' + type_ + '/'),
    get: (name) => api('/api/' + type_ + '/' + encodeURIComponent(name)),
    put: (name, content) => api('/api/' + type_ + '/' + encodeURIComponent(name), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content }),
    }),
    del: (name) => api('/api/' + type_ + '/' + encodeURIComponent(name), { method: 'DELETE' }),
  };
}

const resourceTypes = {
  memories: { api: resourceAPI('memories'), label: 'memory' },
  tools:    { api: resourceAPI('tools'),    label: 'tool' },
  skills:   { api: resourceAPI('skills'),   label: 'skill' },
};

function loadResourceList(type_) {
  const rt = resourceTypes[type_];
  rt.api.list().then(r => {
    const el = document.getElementById(type_ + 'List');
    const items = r.items || [];
    if (items.length === 0) {
      el.innerHTML = '<div class="resource-empty">No ' + type_ + ' yet</div>';
      return;
    }
    el.innerHTML = items.map(item => `
      <div class="resource-item">
        <span class="resource-name">${esc(item.name)}</span>
        <span class="resource-meta">${formatSize(item.size)}</span>
        <button class="btn-sm" onclick="editResource('${type_}','${esc(item.name)}')">Edit</button>
        <button class="btn-sm btn-danger" onclick="deleteResource('${type_}','${esc(item.name)}')">Delete</button>
      </div>
    `).join('');
  }).catch(() => {
    document.getElementById(type_ + 'List').textContent = 'Failed to load';
  });
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  return (bytes / 1024).toFixed(1) + ' KB';
}

function newResource(type_) {
  const editor = document.getElementById(type_ + 'Editor');
  document.getElementById(type_ + 'Name').value = '';
  document.getElementById(type_ + 'Name').disabled = false;
  document.getElementById(type_ + 'Content').value = '';
  editor.style.display = 'block';
}

function editResource(type_, name) {
  const rt = resourceTypes[type_];
  rt.api.get(name).then(r => {
    const editor = document.getElementById(type_ + 'Editor');
    document.getElementById(type_ + 'Name').value = r.name;
    document.getElementById(type_ + 'Name').disabled = true;
    document.getElementById(type_ + 'Content').value = r.content;
    editor.style.display = 'block';
  }).catch(e => toast(e.error || 'Failed to load', 'error'));
}

function closeEditor(type_) {
  document.getElementById(type_ + 'Editor').style.display = 'none';
}

function saveResource(type_) {
  const name = document.getElementById(type_ + 'Name').value.trim();
  const content = document.getElementById(type_ + 'Content').value;
  if (!name) { toast('Name is required', 'error'); return; }
  const rt = resourceTypes[type_];
  rt.api.put(name, content).then(r => {
    if (r.ok) {
      toast(rt.label + ' saved');
      closeEditor(type_);
      loadResourceList(type_);
    } else {
      toast(r.error || 'Save failed', 'error');
    }
  }).catch(e => toast(e.error || 'Save failed', 'error'));
}

function deleteResource(type_, name) {
  if (!confirm('Delete "' + name + '"?')) return;
  const rt = resourceTypes[type_];
  rt.api.del(name).then(r => {
    if (r.ok) {
      toast(rt.label + ' deleted');
      loadResourceList(type_);
    } else {
      toast(r.error || 'Delete failed', 'error');
    }
  }).catch(e => toast(e.error || 'Delete failed', 'error'));
}

// Wire save buttons
document.getElementById('saveMemoryBtn').onclick = () => saveResource('memories');
document.getElementById('saveToolBtn').onclick = () => saveResource('tools');
document.getElementById('saveSkillBtn').onclick = () => saveResource('skills');

// --- Init ---
loadStatus();
loadConfig();
loadRecentLogs();
loadResourceList('memories');
loadResourceList('tools');
loadResourceList('skills');
setInterval(loadStatus, 30000);
setInterval(loadRecentLogs, 10000);
