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
  if (adv) { loadLogFiles(); }
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
    document.getElementById('cfgLogLevel').textContent = cfg.log_level || 'info';
    const qs = cfg.quiet_hours?.start || 0;
    const qe = cfg.quiet_hours?.end || 0;
    document.getElementById('cfgQuietHours').textContent = (qs === 0 && qe === 0) ? 'Disabled' : qs + ':00 — ' + qe + ':00';
    document.getElementById('cfgSystemPrompt').textContent = cfg.system_prompt || '(default)';
  }).catch(() => {});
}

// --- Raw Config (editable) ---
function loadRawConfig() {
  api('/api/config').then(cfg => {
    document.getElementById('rawConfigEditor').value = JSON.stringify(cfg, null, 2);
  }).catch(() => {});
}

document.getElementById('saveConfigBtn').addEventListener('click', () => {
  const editor = document.getElementById('rawConfigEditor');
  let parsed;
  try {
    parsed = JSON.parse(editor.value);
  } catch (e) {
    toast('Invalid JSON: ' + e.message, 'error');
    return;
  }
  api('/api/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(parsed),
  }).then(r => {
    if (r.ok) {
      toast('Config saved');
      loadConfig();
    } else {
      toast(r.error || 'Save failed', 'error');
    }
  }).catch(e => toast(e.error || 'Save failed', 'error'));
});

// --- Tiers (editable) ---
const ALLOWED_MODELS = ['haiku', 'sonnet', 'opus'];
let tiersConfig = {};  // full TiersConfig (router_model, default_fallback, etc.)
let tiersData = [];    // shortcut to tiersConfig.tiers

function loadTiers() {
  api('/api/tiers').then(t => {
    tiersConfig = { ...t };
    tiersData = (t.tiers || []).map(tier => ({ ...tier }));
    tiersConfig.tiers = tiersData;
    renderTiers();
  }).catch(() => {});
}

function renderTiers() {
  const body = document.getElementById('tiersBody');
  body.innerHTML = '';

  // Sync router model dropdown.
  const routerSelect = document.getElementById('routerModelSelect');
  routerSelect.value = tiersConfig.router_model || 'haiku';

  tiersData.forEach((tier, i) => {
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td data-label="Name"><input type="text" value="${esc(tier.name)}" data-idx="${i}" data-field="name" class="tier-input"></td>
      <td data-label="Model">
        <select data-idx="${i}" data-field="model" class="tier-input">
          ${ALLOWED_MODELS.map(m => `<option value="${m}"${m === tier.model ? ' selected' : ''}>${m}</option>`).join('')}
        </select>
      </td>
      <td data-label="Priority"><input type="number" value="${tier.priority}" data-idx="${i}" data-field="priority" class="tier-input" style="width:60px"></td>
      <td data-label="Enabled"><input type="checkbox" ${tier.enabled ? 'checked' : ''} data-idx="${i}" data-field="enabled" class="tier-input"></td>
      <td data-label="Routable"><input type="checkbox" ${tier.routable ? 'checked' : ''} data-idx="${i}" data-field="routable" class="tier-input"></td>
      <td><button class="btn-sm" data-idx="${i}" onclick="openTierModal(${i})">Edit</button></td>
      <td><button class="btn-sm btn-danger" data-idx="${i}" class="tier-delete">✕</button></td>
    `;
    body.appendChild(tr);
  });

  // Bind change events
  body.querySelectorAll('.tier-input').forEach(el => {
    el.addEventListener('change', (e) => {
      const idx = parseInt(e.target.dataset.idx);
      const field = e.target.dataset.field;
      if (field === 'enabled' || field === 'routable') {
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

// Router model dropdown binding.
document.getElementById('routerModelSelect').addEventListener('change', (e) => {
  tiersConfig.router_model = e.target.value;
});

document.getElementById('addTierBtn').addEventListener('click', () => {
  tiersData.push({ name: '', model: 'sonnet', priority: 0, enabled: true, routable: true, instant: false, router_label: '', description: '', max_turns: 0, write_capable: false, tools: [], effort: '', force_command: false });
  renderTiers();
});

document.getElementById('saveTiersBtn').addEventListener('click', () => {
  for (const t of tiersData) {
    if (!t.name.trim()) { toast('All tiers must have a name', 'error'); return; }
  }
  tiersConfig.tiers = tiersData;
  api('/api/tiers', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(tiersConfig),
  }).then(r => {
    if (r.ok) toast('Tiers saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
});

// --- Tier Detail Modal ---
const CLAUDE_TOOLS = ['Bash', 'Read', 'Edit', 'Write', 'Glob', 'Grep', 'WebFetch', 'WebSearch', 'Task', 'MCP'];
let modalIdx = -1;

function renderToolGrid(selected) {
  const grid = document.getElementById('tmTools');
  grid.innerHTML = CLAUDE_TOOLS.map(tool => {
    const checked = selected.includes(tool) ? ' checked' : '';
    return `<label class="tool-check"><input type="checkbox" value="${tool}"${checked}><span>${tool}</span></label>`;
  }).join('');
}

function getSelectedTools() {
  return Array.from(document.querySelectorAll('#tmTools input:checked')).map(el => el.value);
}

let tierSkillFile = null; // currently selected tier skill

function openTierModal(idx) {
  modalIdx = idx;
  const t = tiersData[idx];
  document.getElementById('tierModalTitle').textContent = 'Edit: ' + (t.name || 'Tier');
  document.getElementById('tmRouterLabel').value = t.router_label || '';
  document.getElementById('tmDescription').value = t.description || '';
  document.getElementById('tmMaxTurns').value = t.max_turns || 0;
  document.getElementById('tmEffort').value = t.effort || '';
  document.getElementById('tmWriteCapable').checked = !!t.write_capable;
  document.getElementById('tmForceCommand').checked = !!t.force_command;
  document.getElementById('tmInstant').checked = !!t.instant;
  renderToolGrid(t.tools || []);
  // Reset to General tab.
  switchModalTab('general');
  document.getElementById('tierModal').classList.add('open');
}

function closeTierModal() {
  document.getElementById('tierModal').classList.remove('open');
  modalIdx = -1;
  tierSkillFile = null;
}

// Modal tab switching.
function switchModalTab(tabName) {
  document.querySelectorAll('.modal-tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tabName));
  document.querySelectorAll('.modal-tab-content').forEach(c => c.classList.toggle('active', c.id === 'tab-' + tabName));
  // Load data for tab.
  if (tabName === 'sysprompt' && modalIdx >= 0) loadTierSystemPrompt(tiersData[modalIdx].name);
  if (tabName === 'skills' && modalIdx >= 0) loadTierSkills(tiersData[modalIdx].name);
}

document.querySelectorAll('.modal-tab').forEach(tab => {
  tab.addEventListener('click', () => switchModalTab(tab.dataset.tab));
});

// Tier System Prompt.
function loadTierSystemPrompt(tierName) {
  if (!tierName) return;
  api('/api/tiers/' + encodeURIComponent(tierName) + '/system-prompt').then(r => {
    document.getElementById('tmSystemPrompt').value = r.content || '';
  }).catch(() => {
    document.getElementById('tmSystemPrompt').value = '';
  });
}

document.getElementById('saveTierSysPrompt').onclick = () => {
  if (modalIdx < 0) return;
  const tierName = tiersData[modalIdx].name;
  if (!tierName) { toast('Save the tier first', 'error'); return; }
  const content = document.getElementById('tmSystemPrompt').value;
  api('/api/tiers/' + encodeURIComponent(tierName) + '/system-prompt', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  }).then(r => {
    if (r.ok) toast('System prompt saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
};

// Tier Skills mini-explorer.
function loadTierSkills(tierName) {
  if (!tierName) return;
  tierSkillFile = null;
  document.getElementById('tierSkillEditor').value = '';
  document.getElementById('tierSkillEditor').disabled = true;
  document.getElementById('tierSkillName').textContent = 'Select a skill';
  document.getElementById('saveTierSkillBtn').disabled = true;
  document.getElementById('deleteTierSkillBtn').disabled = true;

  api('/api/tiers/' + encodeURIComponent(tierName) + '/skills/').then(r => {
    const list = document.getElementById('tierSkillList');
    const items = r.items || [];
    if (items.length === 0) {
      list.innerHTML = '<div class="explorer-empty">No skills</div>';
      return;
    }
    list.innerHTML = items.map(f =>
      '<div class="explorer-file-item" data-name="' + esc(f.name) + '">' +
        '<span class="explorer-file-name">' + esc(f.name) + '</span>' +
        '<span class="explorer-file-size">' + formatSize(f.size) + '</span>' +
      '</div>'
    ).join('');
    list.querySelectorAll('.explorer-file-item').forEach(el => {
      el.addEventListener('click', () => selectTierSkill(tierName, el.dataset.name));
    });
  }).catch(() => {
    document.getElementById('tierSkillList').innerHTML = '<div class="explorer-empty">Failed to load</div>';
  });
}

function selectTierSkill(tierName, name) {
  tierSkillFile = name;
  document.getElementById('tierSkillName').textContent = name;
  document.getElementById('saveTierSkillBtn').disabled = false;
  document.getElementById('deleteTierSkillBtn').disabled = false;
  document.querySelectorAll('#tierSkillList .explorer-file-item').forEach(el => {
    el.classList.toggle('active', el.dataset.name === name);
  });
  api('/api/tiers/' + encodeURIComponent(tierName) + '/skills/' + encodeURIComponent(name)).then(r => {
    document.getElementById('tierSkillEditor').value = r.content || '';
    document.getElementById('tierSkillEditor').disabled = false;
  }).catch(() => toast('Failed to load skill', 'error'));
}

document.getElementById('saveTierSkillBtn').onclick = () => {
  if (modalIdx < 0 || !tierSkillFile) return;
  const tierName = tiersData[modalIdx].name;
  const content = document.getElementById('tierSkillEditor').value;
  api('/api/tiers/' + encodeURIComponent(tierName) + '/skills/' + encodeURIComponent(tierSkillFile), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  }).then(r => {
    if (r.ok) { toast('Skill saved'); loadTierSkills(tierName); }
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
};

document.getElementById('newTierSkillBtn').onclick = () => {
  if (modalIdx < 0) return;
  const tierName = tiersData[modalIdx].name;
  if (!tierName) { toast('Save the tier first', 'error'); return; }
  const name = prompt('Skill name (e.g. code-review):');
  if (!name || !name.trim()) return;
  api('/api/tiers/' + encodeURIComponent(tierName) + '/skills/' + encodeURIComponent(name.trim()), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content: '' }),
  }).then(r => {
    if (r.ok) { toast('Created'); loadTierSkills(tierName); }
    else toast(r.error || 'Create failed', 'error');
  }).catch(e => toast(e.error || 'Create failed', 'error'));
};

document.getElementById('deleteTierSkillBtn').onclick = () => {
  if (modalIdx < 0 || !tierSkillFile) return;
  if (!confirm('Delete skill ' + tierSkillFile + '?')) return;
  const tierName = tiersData[modalIdx].name;
  api('/api/tiers/' + encodeURIComponent(tierName) + '/skills/' + encodeURIComponent(tierSkillFile), {
    method: 'DELETE',
  }).then(r => {
    if (r.ok) { toast('Deleted'); loadTierSkills(tierName); }
    else toast(r.error || 'Delete failed', 'error');
  }).catch(e => toast(e.error || 'Delete failed', 'error'));
};

document.getElementById('applyTierModal').onclick = () => {
  if (modalIdx < 0) return;
  const t = tiersData[modalIdx];
  t.router_label = document.getElementById('tmRouterLabel').value;
  t.description = document.getElementById('tmDescription').value;
  t.max_turns = parseInt(document.getElementById('tmMaxTurns').value) || 0;
  t.effort = document.getElementById('tmEffort').value;
  t.write_capable = document.getElementById('tmWriteCapable').checked;
  t.force_command = document.getElementById('tmForceCommand').checked;
  t.instant = document.getElementById('tmInstant').checked;
  t.tools = getSelectedTools();
  closeTierModal();
};

document.getElementById('closeTierModal').onclick = closeTierModal;
document.getElementById('tierModal').onclick = (e) => {
  if (e.target.id === 'tierModal') closeTierModal();
};

function esc(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// --- File Explorer ---
let explorerType = 'memories';
let explorerFile = null;  // currently selected filename

function loadExplorer(type) {
  explorerType = type || explorerType;
  explorerFile = null;
  document.getElementById('explorerEditor').value = '';
  document.getElementById('explorerEditor').disabled = true;
  document.getElementById('explorerFileName').textContent = 'Select a file';
  document.getElementById('saveFileBtn').disabled = true;
  document.getElementById('deleteFileBtn').disabled = true;

  // Update active tab.
  document.querySelectorAll('.explorer-tab').forEach(t => {
    t.classList.toggle('active', t.dataset.type === explorerType);
  });

  api('/api/' + explorerType + '/').then(r => {
    const list = document.getElementById('explorerFileList');
    const items = r.items || [];
    if (items.length === 0) {
      list.innerHTML = '<div class="explorer-empty">No files</div>';
      return;
    }
    list.innerHTML = items.map(f =>
      '<div class="explorer-file-item" data-name="' + esc(f.name) + '">' +
        '<span class="explorer-file-name">' + esc(f.name) + '</span>' +
        '<span class="explorer-file-size">' + formatSize(f.size) + '</span>' +
      '</div>'
    ).join('');
    // Bind click handlers.
    list.querySelectorAll('.explorer-file-item').forEach(el => {
      el.addEventListener('click', () => selectFile(explorerType, el.dataset.name));
    });
  }).catch(() => {
    document.getElementById('explorerFileList').innerHTML = '<div class="explorer-empty">Failed to load</div>';
  });
}

function selectFile(type, name) {
  explorerFile = name;
  document.getElementById('explorerFileName').textContent = name;
  document.getElementById('saveFileBtn').disabled = false;
  document.getElementById('deleteFileBtn').disabled = false;

  // Highlight active file.
  document.querySelectorAll('.explorer-file-item').forEach(el => {
    el.classList.toggle('active', el.dataset.name === name);
  });

  api('/api/' + type + '/' + encodeURIComponent(name)).then(r => {
    const editor = document.getElementById('explorerEditor');
    editor.value = r.content || '';
    editor.disabled = false;
  }).catch(() => {
    toast('Failed to load file', 'error');
  });
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B';
  return (bytes / 1024).toFixed(1) + ' KB';
}

document.getElementById('saveFileBtn').addEventListener('click', () => {
  if (!explorerFile) return;
  const content = document.getElementById('explorerEditor').value;
  api('/api/' + explorerType + '/' + encodeURIComponent(explorerFile), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  }).then(r => {
    if (r.ok) { toast('Saved'); loadExplorer(); }
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
});

document.getElementById('newFileBtn').addEventListener('click', () => {
  const name = prompt('File name (e.g. notes.md):');
  if (!name || !name.trim()) return;
  api('/api/' + explorerType + '/' + encodeURIComponent(name.trim()), {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content: '' }),
  }).then(r => {
    if (r.ok) {
      toast('Created');
      loadExplorer();
      setTimeout(() => selectFile(explorerType, name.trim()), 300);
    } else toast(r.error || 'Create failed', 'error');
  }).catch(e => toast(e.error || 'Create failed', 'error'));
});

document.getElementById('deleteFileBtn').addEventListener('click', () => {
  if (!explorerFile) return;
  if (!confirm('Delete ' + explorerFile + '?')) return;
  api('/api/' + explorerType + '/' + encodeURIComponent(explorerFile), {
    method: 'DELETE',
  }).then(r => {
    if (r.ok) { toast('Deleted'); loadExplorer(); }
    else toast(r.error || 'Delete failed', 'error');
  }).catch(e => toast(e.error || 'Delete failed', 'error'));
});

// Tab click handlers.
document.querySelectorAll('.explorer-tab').forEach(tab => {
  tab.addEventListener('click', () => loadExplorer(tab.dataset.type));
});

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

// --- Router Prompt ---
function loadRouterPrompt() {
  api('/api/router-prompt').then(r => {
    document.getElementById('routerPromptEditor').value = r.content || '';
  }).catch(() => {});
}

document.getElementById('saveRouterPromptBtn').addEventListener('click', () => {
  const content = document.getElementById('routerPromptEditor').value;
  api('/api/router-prompt', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  }).then(r => {
    if (r.ok) toast('Router prompt saved');
    else toast(r.error || 'Save failed', 'error');
  }).catch(e => toast(e.error || 'Save failed', 'error'));
});

// --- Init ---
loadStatus();
loadConfig();
loadRawConfig();
loadTiers();
loadExplorer('memories');
loadRouterPrompt();
loadRecentLogs();
setInterval(loadStatus, 30000);
setInterval(loadRecentLogs, 10000);
