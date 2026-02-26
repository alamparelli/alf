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
  if (adv) { loadRawConfig(); loadLogFiles(); }
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

// --- Tiers (editable) ---
const ALLOWED_MODELS = ['haiku', 'sonnet', 'opus'];
let tiersData = [];

function loadTiers() {
  api('/api/tiers').then(t => {
    tiersData = (t.tiers || []).map(tier => ({ ...tier }));
    renderTiers();
  }).catch(() => {});
}

function renderTiers() {
  const body = document.getElementById('tiersBody');
  body.innerHTML = '';
  tiersData.forEach((tier, i) => {
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td><input type="text" value="${esc(tier.name)}" data-idx="${i}" data-field="name" class="tier-input"></td>
      <td>
        <select data-idx="${i}" data-field="model" class="tier-input">
          ${ALLOWED_MODELS.map(m => `<option value="${m}"${m === tier.model ? ' selected' : ''}>${m}</option>`).join('')}
        </select>
      </td>
      <td><input type="number" value="${tier.priority}" data-idx="${i}" data-field="priority" class="tier-input" style="width:60px"></td>
      <td><input type="checkbox" ${tier.enabled ? 'checked' : ''} data-idx="${i}" data-field="enabled" class="tier-input"></td>
      <td><button class="btn-sm btn-danger" data-idx="${i}" class="tier-delete">✕</button></td>
    `;
    body.appendChild(tr);
  });

  // Bind change events
  body.querySelectorAll('.tier-input').forEach(el => {
    el.addEventListener('change', (e) => {
      const idx = parseInt(e.target.dataset.idx);
      const field = e.target.dataset.field;
      if (field === 'enabled') {
        tiersData[idx][field] = e.target.checked;
      } else if (field === 'priority') {
        tiersData[idx][field] = parseInt(e.target.value) || 0;
      } else {
        tiersData[idx][field] = e.target.value;
      }
    });
  });

  // Bind delete buttons
  body.querySelectorAll('.btn-danger').forEach(el => {
    el.addEventListener('click', (e) => {
      const idx = parseInt(e.target.dataset.idx);
      if (tiersData.length <= 1) { toast('At least one tier is required', 'error'); return; }
      tiersData.splice(idx, 1);
      renderTiers();
    });
  });
}

document.getElementById('addTierBtn').addEventListener('click', () => {
  tiersData.push({ name: '', model: 'sonnet', priority: 0, enabled: true });
  renderTiers();
});

document.getElementById('saveTiersBtn').addEventListener('click', () => {
  for (const t of tiersData) {
    if (!t.name.trim()) { toast('All tiers must have a name', 'error'); return; }
  }
  api('/api/tiers', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tiers: tiersData }),
  }).then(r => {
    if (r.ok) toast('Tiers saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
});

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

// --- Init ---
loadStatus();
loadConfig();
loadTiers();
loadRecentLogs();
setInterval(loadStatus, 30000);
setInterval(loadRecentLogs, 10000);
