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
  const headers = { 'Authorization': 'Bearer ' + TOKEN, ...(opts.headers || {}) };
  return fetch(path, { ...opts, headers }).then(r => {
    if (r.status === 401) { toast('Unauthorized — invalid token', 'error'); throw new Error('401'); }
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

// --- Config ---
let currentConfig = {};
function loadConfig() {
  api('/api/config').then(cfg => {
    currentConfig = cfg;
    document.getElementById('cfgModel').value = cfg.model || 'sonnet';
    document.getElementById('cfgLogLevel').value = cfg.log_level || 'info';
    document.getElementById('cfgQuietStart').value = cfg.quiet_hours?.start || 0;
    document.getElementById('cfgQuietEnd').value = cfg.quiet_hours?.end || 0;
  }).catch(() => {});
}

document.getElementById('saveConfigBtn').onclick = () => {
  const cfg = {
    ...currentConfig,
    model: document.getElementById('cfgModel').value,
    log_level: document.getElementById('cfgLogLevel').value,
    quiet_hours: {
      start: parseInt(document.getElementById('cfgQuietStart').value) || 0,
      end: parseInt(document.getElementById('cfgQuietEnd').value) || 0,
    }
  };
  api('/api/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cfg),
  }).then(r => {
    if (r.ok) toast('Config saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(() => toast('Save failed', 'error'));
};

// --- Raw Config ---
function loadRawConfig() {
  api('/api/config').then(cfg => {
    document.getElementById('rawConfigEditor').value = JSON.stringify(cfg, null, 2);
  }).catch(() => {});
}

document.getElementById('saveRawConfigBtn').onclick = () => {
  const raw = document.getElementById('rawConfigEditor').value;
  try { JSON.parse(raw); } catch (e) { toast('Invalid JSON', 'error'); return; }
  api('/api/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: raw,
  }).then(r => {
    if (r.ok) { toast('Config saved'); loadConfig(); }
    else toast(r.error || 'Save failed', 'error');
  }).catch(() => toast('Save failed', 'error'));
};

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

// --- Tiers ---
let tiersData = { tiers: [] };

function loadTiers() {
  api('/api/tiers').then(t => {
    tiersData = t;
    renderTiers();
  }).catch(() => {});
}

function renderTiers() {
  const body = document.getElementById('tiersBody');
  body.innerHTML = '';
  (tiersData.tiers || []).forEach((tier, i) => {
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td><input value="${tier.name}" data-i="${i}" data-f="name"></td>
      <td><select data-i="${i}" data-f="model">
        <option value="haiku" ${tier.model==='haiku'?'selected':''}>Haiku</option>
        <option value="sonnet" ${tier.model==='sonnet'?'selected':''}>Sonnet</option>
        <option value="opus" ${tier.model==='opus'?'selected':''}>Opus</option>
      </select></td>
      <td><input type="number" value="${tier.priority}" data-i="${i}" data-f="priority" style="width:60px"></td>
      <td><input type="checkbox" ${tier.enabled?'checked':''} data-i="${i}" data-f="enabled"></td>
    `;
    body.appendChild(tr);
  });
  body.querySelectorAll('input,select').forEach(el => {
    el.addEventListener('change', () => {
      const i = parseInt(el.dataset.i);
      const f = el.dataset.f;
      if (f === 'enabled') tiersData.tiers[i][f] = el.checked;
      else if (f === 'priority') tiersData.tiers[i][f] = parseInt(el.value) || 0;
      else tiersData.tiers[i][f] = el.value;
    });
  });
}

document.getElementById('addTierBtn').onclick = () => {
  tiersData.tiers.push({ name: '', model: 'sonnet', priority: 0, enabled: true });
  renderTiers();
};

document.getElementById('saveTiersBtn').onclick = () => {
  api('/api/tiers', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(tiersData),
  }).then(r => {
    if (r.ok) toast('Tiers saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(() => toast('Save failed', 'error'));
};

// --- Init ---
loadStatus();
loadConfig();
loadRecentLogs();
setInterval(loadStatus, 30000);
setInterval(loadRecentLogs, 10000);
