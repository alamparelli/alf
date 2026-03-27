<script lang="ts">
  import { onMount } from 'svelte'
  import { Plus, Trash2, RefreshCw, ShieldOff, Shield } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import { api } from '../lib/api'
  import { events } from '../stores/events.svelte'
  import { toasts } from '../stores/toast.svelte'

  interface Rule {
    pattern: string
    action: string
  }

  interface FirewallConfig {
    mode: string
    port: number
    rules: Rule[]
  }

  interface RequestEntry {
    time: string
    method: string
    host: string
    path: string
    status: number
    blocked: boolean
    rule: string
    source?: string  // "" = direct proxy, "vault" = vault-proxy
  }

  let config = $state<FirewallConfig>({ mode: 'log-only', port: 4751, rules: [] })
  let log = $state<RequestEntry[]>([])
  let autoRefresh = $state(true)
  let loading = $state(false)
  let logFilter = $state('')

  // Hosts sorting
  let hostSortKey = $state<'host' | 'count' | 'allowed' | 'blocked' | 'last_seen'>('count')
  let hostSortAsc = $state(false)

  // Add rule modal
  let showAddRule = $state(false)
  let newPattern = $state('')
  let newAction = $state('allow')

  let refreshTimer: ReturnType<typeof setInterval> | undefined
  let activeTab = $state<'log' | 'hosts'>('log')

  // Pagination
  let pageSize = $state(50)
  let logPage = $state(1)
  let hostsPage = $state(1)

  // Persistent host stats from backend
  let hosts = $state<any[]>([])
  let killSwitch = $state(false)
  let hostsFilter = $state('')

  // Derived: filtered log entries (most recent first)
  let filteredLog = $derived((() => {
    const reversed = [...log].reverse()
    if (!logFilter.trim()) return reversed
    const q = logFilter.toLowerCase()
    return reversed.filter((e: RequestEntry) =>
      e.host.toLowerCase().includes(q) ||
      e.method.toLowerCase().includes(q) ||
      (e.path && e.path.toLowerCase().includes(q)) ||
      (e.source && e.source.toLowerCase().includes(q))
    )
  })())

  // Derived: paginated log
  let logTotalPages = $derived(Math.max(1, Math.ceil(filteredLog.length / pageSize)))
  let clampedLogPage = $derived(Math.min(logPage, logTotalPages))
  let paginatedLog = $derived(filteredLog.slice((clampedLogPage - 1) * pageSize, clampedLogPage * pageSize))

  // Derived: filtered hosts
  let filteredHosts = $derived((() => {
    if (!hostsFilter.trim()) return hosts
    const q = hostsFilter.toLowerCase()
    return hosts.filter((h: any) => h.host.toLowerCase().includes(q))
  })())

  // Derived: sorted hosts
  let sortedHosts = $derived((() => {
    const sorted = [...filteredHosts]
    sorted.sort((a: any, b: any) => {
      let va = a[hostSortKey], vb = b[hostSortKey]
      if (typeof va === 'string') {
        va = va.toLowerCase(); vb = (vb || '').toLowerCase()
        return hostSortAsc ? va.localeCompare(vb) : vb.localeCompare(va)
      }
      return hostSortAsc ? (va || 0) - (vb || 0) : (vb || 0) - (va || 0)
    })
    return sorted
  })())

  // Derived: paginated hosts
  let hostsTotalPages = $derived(Math.max(1, Math.ceil(sortedHosts.length / pageSize)))
  let clampedHostsPage = $derived(Math.min(hostsPage, hostsTotalPages))
  let paginatedHosts = $derived(sortedHosts.slice((clampedHostsPage - 1) * pageSize, clampedHostsPage * pageSize))

  // Reset pages when filter or page size changes
  function setPageSize(size: number) {
    pageSize = size
    logPage = 1
    hostsPage = 1
  }

  function sortHosts(key: typeof hostSortKey) {
    if (hostSortKey === key) {
      hostSortAsc = !hostSortAsc
    } else {
      hostSortKey = key
      hostSortAsc = key === 'host' // alpha ascending by default, numbers descending
    }
  }

  function sortIndicator(key: string): string {
    if (hostSortKey !== key) return ''
    return hostSortAsc ? ' ▲' : ' ▼'
  }

  async function loadFirewall() {
    loading = true
    try {
      const data = await api<any>('/api/firewall')
      config = data.config || { mode: 'log-only', port: 4751, rules: [] }
      log = data.log || []
      hosts = (data.hosts || []).sort((a: any, b: any) => b.count - a.count)
      if (data.kill_switch !== undefined) killSwitch = data.kill_switch
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load firewall', 'error')
    } finally {
      loading = false
    }
  }

  async function saveConfig() {
    try {
      await api('/api/firewall', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config)
      })
      toasts.show('Firewall config saved', 'success')
    } catch (e: any) {
      toasts.show(e.error || 'Save failed', 'error')
    }
  }

  function setMode(mode: string) {
    config.mode = mode
    saveConfig()
  }

  function addRule() {
    if (!newPattern.trim()) return
    config.rules = [...config.rules, { pattern: newPattern.trim(), action: newAction }]
    newPattern = ''
    newAction = 'allow'
    showAddRule = false
    saveConfig()
  }

  function removeRule(index: number) {
    config.rules = config.rules.filter((_, i) => i !== index)
    saveConfig()
  }

  function addDenyRule(host: string) {
    if (config.rules.some(r => r.pattern === host)) {
      toasts.show('Rule already exists for ' + host, 'info')
      return
    }
    config.rules = [...config.rules, { pattern: host, action: 'deny' }]
    saveConfig()
  }

  function addAllowRule(host: string) {
    if (config.rules.some(r => r.pattern === host)) {
      toasts.show('Rule already exists for ' + host, 'info')
      return
    }
    config.rules = [...config.rules, { pattern: host, action: 'allow' }]
    saveConfig()
  }

  async function toggleKillSwitch() {
    const enabling = !killSwitch
    if (enabling && !confirm('Block ALL outbound network traffic? This will prevent all external connections.')) {
      return
    }
    try {
      const result = await api<any>('/api/firewall/killswitch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: enabling })
      })
      killSwitch = result.enabled
      toasts.show(killSwitch ? 'Kill switch ENABLED — all traffic blocked' : 'Kill switch disabled', killSwitch ? 'error' : 'success')
    } catch (e: any) {
      toasts.show(e.error || 'Failed to toggle kill switch', 'error')
    }
  }

  async function clearLog() {
    try {
      await api('/api/firewall', { method: 'DELETE' })
      log = []
      toasts.show('Log cleared', 'success')
    } catch (e: any) {
      toasts.show(e.error || 'Failed to clear log', 'error')
    }
  }

  function formatTime(iso: string): string {
    try {
      const d = new Date(iso)
      return d.toLocaleTimeString()
    } catch {
      return iso
    }
  }

  function formatDateTime(iso: string): string {
    try {
      const d = new Date(iso)
      return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString()
    } catch {
      return iso
    }
  }

  let unsubEvents: (() => void) | undefined

  onMount(() => {
    loadFirewall()
    unsubEvents = events.subscribe('firewall', loadFirewall)
    return () => { unsubEvents?.() }
  })
</script>

<div class="firewall-view">
  <h2>Firewall</h2>

  <!-- Mode toggle + Kill switch -->
  <Card>
    <div class="mode-section">
      <h3>Mode</h3>
      <div class="mode-controls">
        <div class="segmented">
          <button
            class="seg-btn"
            class:active={config.mode === 'log-only'}
            onclick={() => setMode('log-only')}
          >
            <ShieldOff size={14} /> Log Only
          </button>
          <button
            class="seg-btn"
            class:active={config.mode === 'enforce'}
            onclick={() => setMode('enforce')}
          >
            <Shield size={14} /> Enforce
          </button>
        </div>
        <button
          class="kill-switch-btn"
          class:active={killSwitch}
          onclick={toggleKillSwitch}
          title={killSwitch ? 'Click to resume network' : 'Block ALL outbound traffic immediately'}
        >
          {killSwitch ? '🔴 Kill Switch ON' : 'Kill Switch'}
        </button>
      </div>
    </div>
  </Card>

  <!-- Rules -->
  <Card>
    <div class="rules-header">
      <h3>Rules</h3>
      <button class="btn-primary" onclick={() => showAddRule = true}>
        <Plus size={14} /> Add Rule
      </button>
    </div>

    {#if config.rules.length > 0}
      <div class="rules-list">
        {#each config.rules as rule, i}
          <div class="rule-row">
            <code class="rule-pattern">{rule.pattern}</code>
            <span class="action-badge" class:allow={rule.action === 'allow'} class:deny={rule.action === 'deny'}>
              {rule.action}
            </span>
            <button class="btn-icon-sm" onclick={() => removeRule(i)} title="Delete rule">
              <Trash2 size={14} />
            </button>
          </div>
        {/each}
      </div>
    {:else}
      <p class="empty-sm">No rules configured</p>
    {/if}
  </Card>

  <!-- Request log / Hosts -->
  <Card>
    <div class="log-header">
      <div class="log-tabs">
        <button class="log-tab" class:active={activeTab === 'log'} onclick={() => activeTab = 'log'}>Request Log</button>
        <button class="log-tab" class:active={activeTab === 'hosts'} onclick={() => activeTab = 'hosts'}>Hosts ({hosts.length})</button>
      </div>
      <div class="log-controls">
        <label class="auto-refresh">
          <input type="checkbox" bind:checked={autoRefresh} />
          Auto-refresh
        </label>
        <button class="btn-icon" onclick={() => loadFirewall()} title="Refresh">
          <RefreshCw size={14} />
        </button>
        <button class="btn-secondary-sm" onclick={clearLog}>Clear</button>
      </div>
    </div>

    {#if activeTab === 'log'}
      <div class="filter-bar">
        <input type="text" class="filter-input" placeholder="Filter by host, method, source..." bind:value={logFilter} />
        {#if logFilter}<span class="filter-count">{filteredLog.length} / {log.length}</span>{/if}
      </div>
      {#if filteredLog.length > 0}
        <div class="log-table-wrap">
          <table class="log-table">
            <thead>
              <tr>
                <th>Time</th>
                <th>Method</th>
                <th>Host</th>
                <th>Path</th>
                <th>Status</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {#each paginatedLog as entry}
                <tr class:blocked={entry.blocked}>
                  <td class="mono">{formatTime(entry.time)}</td>
                  <td><span class="method-badge">{entry.method}</span></td>
                  <td class="mono">{entry.host}{#if entry.source} <span class="source-badge {entry.source}">{entry.source}</span>{/if}</td>
                  <td class="mono path-cell">{entry.path}</td>
                  <td>
                    <span class="status-badge" class:status-ok={entry.status >= 200 && entry.status < 400} class:status-blocked={entry.blocked}>
                      {entry.blocked ? 'DENIED' : entry.status || 'OK'}
                    </span>
                  </td>
                  <td class="action-cell">
                    <button class="btn-icon-sm btn-allow" onclick={() => addAllowRule(entry.host)} title="Allow this host">
                      <ShieldOff size={12} />
                    </button>
                    <button class="btn-icon-sm btn-deny" onclick={() => addDenyRule(entry.host)} title="Deny this host">
                      <Shield size={12} />
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <div class="pagination">
          <div class="page-size">
            {#each [50, 100, 200] as size}
              <button class="page-size-btn" class:active={pageSize === size} onclick={() => setPageSize(size)}>{size}</button>
            {/each}
          </div>
          <span class="page-info">{filteredLog.length} entries</span>
          {#if logTotalPages > 1}
            <div class="page-nav">
              <button class="page-btn" disabled={logPage <= 1} onclick={() => logPage--}>&laquo;</button>
              <span class="page-info">{logPage} / {logTotalPages}</span>
              <button class="page-btn" disabled={logPage >= logTotalPages} onclick={() => logPage++}>&raquo;</button>
            </div>
          {/if}
        </div>
      {:else}
        <p class="empty-sm">{loading ? 'Loading...' : 'No requests logged'}</p>
      {/if}
    {:else}
      <div class="filter-bar">
        <input type="text" class="filter-input" placeholder="Filter by host..." bind:value={hostsFilter} />
        {#if hostsFilter}<span class="filter-count">{filteredHosts.length} / {hosts.length}</span>{/if}
      </div>
      {#if filteredHosts.length > 0}
        <div class="log-table-wrap">
          <table class="log-table">
            <thead>
              <tr>
                <th class="sortable" onclick={() => sortHosts('host')}>Host{sortIndicator('host')}</th>
                <th class="sortable" onclick={() => sortHosts('count')}>Requests{sortIndicator('count')}</th>
                <th class="sortable" onclick={() => sortHosts('allowed')}>Allowed{sortIndicator('allowed')}</th>
                <th class="sortable" onclick={() => sortHosts('blocked')}>Denied{sortIndicator('blocked')}</th>
                <th class="sortable" onclick={() => sortHosts('last_seen')}>Last seen{sortIndicator('last_seen')}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {#each paginatedHosts as h}
                <tr class:blocked={h.blocked > 0 && h.allowed === 0}>
                  <td class="mono">{h.host}{#if h.vault} <span class="source-badge">vault</span>{/if}</td>
                  <td>{h.count}</td>
                  <td>{h.allowed}</td>
                  <td>{h.blocked || ''}</td>
                  <td class="mono">{formatDateTime(h.last_seen)}</td>
                  <td class="action-cell">
                    <button class="btn-icon-sm btn-allow" onclick={() => addAllowRule(h.host)} title="Allow this host">
                      <ShieldOff size={12} />
                    </button>
                    <button class="btn-icon-sm btn-deny" onclick={() => addDenyRule(h.host)} title="Deny this host">
                      <Shield size={12} />
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <div class="pagination">
          <div class="page-size">
            {#each [50, 100, 200] as size}
              <button class="page-size-btn" class:active={pageSize === size} onclick={() => setPageSize(size)}>{size}</button>
            {/each}
          </div>
          <span class="page-info">{filteredHosts.length} hosts</span>
          {#if hostsTotalPages > 1}
            <div class="page-nav">
              <button class="page-btn" disabled={hostsPage <= 1} onclick={() => hostsPage--}>&laquo;</button>
              <span class="page-info">{hostsPage} / {hostsTotalPages}</span>
              <button class="page-btn" disabled={hostsPage >= hostsTotalPages} onclick={() => hostsPage++}>&raquo;</button>
            </div>
          {/if}
        </div>
      {:else}
        <p class="empty-sm">No hosts recorded yet</p>
      {/if}
    {/if}
  </Card>

  <!-- Add Rule Modal -->
  <Modal open={showAddRule} onclose={() => showAddRule = false}>
    <h3>Add Rule</h3>
    <div class="modal-form">
      <label>
        Pattern
        <input type="text" bind:value={newPattern} placeholder="*.example.com" />
      </label>
      <label>
        Action
        <select bind:value={newAction}>
          <option value="allow">Allow</option>
          <option value="deny">Deny</option>
        </select>
      </label>
      <div class="modal-actions">
        <button class="btn-primary" onclick={addRule}>Add</button>
        <button class="btn-secondary" onclick={() => showAddRule = false}>Cancel</button>
      </div>
    </div>
  </Modal>
</div>

<style>
  .firewall-view {
    padding: 8px 0;
    width: 100%;
  }

  h2 {
    margin-bottom: 16px;
  }

  h3 {
    font-size: 1rem;
    margin-bottom: 0;
  }

  .mode-section {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .mode-controls {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .kill-switch-btn {
    padding: 6px 14px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: none;
    color: var(--text-dim);
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }

  .kill-switch-btn:hover {
    border-color: var(--red, #e55);
    color: var(--red, #e55);
  }

  .kill-switch-btn.active {
    background: rgba(230, 80, 80, 0.15);
    border-color: var(--red, #e55);
    color: var(--red, #e55);
  }

  .segmented {
    display: inline-flex;
    border: 1px solid var(--border);
    border-radius: 6px;
    overflow: hidden;
  }

  .seg-btn {
    display: flex;
    align-items: center;
    gap: 4px;
    background: none;
    border: none;
    color: var(--text-dim);
    padding: 6px 16px;
    font-size: 0.85rem;
    cursor: pointer;
    border-right: 1px solid var(--border);
  }

  .seg-btn:last-child {
    border-right: none;
  }

  .seg-btn.active {
    background: var(--accent);
    color: var(--bg);
  }

  .rules-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.75rem;
  }

  .btn-primary {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: var(--accent);
    color: var(--bg);
    border: none;
    border-radius: 6px;
    padding: 6px 14px;
    font-size: 0.8rem;
    font-weight: 600;
    cursor: pointer;
  }

  .btn-primary:hover {
    opacity: 0.9;
  }

  .btn-secondary {
    background: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    padding: 6px 14px;
    font-size: 0.8rem;
    cursor: pointer;
  }

  .btn-secondary-sm {
    background: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    padding: 4px 10px;
    font-size: 0.75rem;
    cursor: pointer;
  }

  .btn-secondary-sm:hover {
    color: var(--text);
    border-color: var(--text-dim);
  }

  .btn-icon {
    background: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    padding: 4px;
    cursor: pointer;
    display: flex;
    align-items: center;
  }

  .btn-icon:hover {
    color: var(--accent);
    border-color: var(--accent);
  }

  .btn-icon-sm {
    background: none;
    border: none;
    color: var(--text-dim);
    padding: 2px;
    cursor: pointer;
    display: flex;
    align-items: center;
  }

  .btn-icon-sm.btn-allow:hover {
    color: var(--green, #6a6);
  }

  .btn-icon-sm.btn-deny:hover {
    color: var(--red, #e55);
  }

  .action-cell {
    display: flex;
    gap: 2px;
  }

  .rules-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .rule-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 8px;
    background: var(--bg);
    border-radius: 6px;
  }

  .rule-pattern {
    flex: 1;
    font-size: 0.85rem;
  }

  .action-badge {
    font-size: 0.7rem;
    font-weight: 600;
    padding: 2px 8px;
    border-radius: 4px;
    text-transform: uppercase;
  }

  .action-badge.allow {
    background: rgba(100, 200, 100, 0.15);
    color: var(--green, #6a6);
  }

  .action-badge.deny {
    background: rgba(230, 80, 80, 0.15);
    color: var(--red, #e55);
  }

  .log-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 0.75rem;
  }

  .log-tabs {
    display: flex;
    gap: 0;
  }

  .log-tab {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-dim);
    font-size: 0.9rem;
    font-weight: 500;
    padding: 4px 12px 6px;
    cursor: pointer;
  }

  .log-tab.active {
    color: var(--text);
    border-bottom-color: var(--accent);
  }

  .log-tab:hover:not(.active) {
    color: var(--text);
  }

  .log-controls {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .auto-refresh {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 0.8rem;
    color: var(--text-dim);
    cursor: pointer;
  }

  .filter-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 8px;
  }

  .filter-input {
    flex: 1;
    padding: 5px 10px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg);
    color: var(--text);
    font-size: 0.8rem;
    font-family: 'JetBrains Mono', monospace;
  }

  .filter-count {
    font-size: 0.75rem;
    color: var(--text-dim);
    white-space: nowrap;
  }

  .sortable {
    cursor: pointer;
    user-select: none;
  }

  .sortable:hover {
    color: var(--text);
  }

  .log-table-wrap {
    overflow-x: auto;
  }

  .log-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.8rem;
  }

  .log-table th {
    text-align: left;
    color: var(--text-dim);
    font-size: 0.72rem;
    text-transform: uppercase;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
  }

  .log-table td {
    padding: 5px 8px;
    border-bottom: 1px solid var(--border);
  }

  .log-table tr.blocked {
    background: rgba(230, 80, 80, 0.05);
  }

  .mono {
    font-family: 'JetBrains Mono', 'Fira Code', monospace;
    font-size: 0.78rem;
  }

  .path-cell {
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .method-badge {
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--accent);
  }

  .status-badge {
    font-size: 0.7rem;
    font-weight: 600;
    padding: 1px 6px;
    border-radius: 3px;
  }

  .status-ok {
    color: var(--green, #6a6);
  }

  .status-blocked {
    background: rgba(230, 80, 80, 0.15);
    color: var(--red, #e55);
  }

  .source-badge {
    font-size: 0.6rem;
    font-weight: 600;
    padding: 1px 5px;
    border-radius: 3px;
    background: rgba(160, 120, 220, 0.15);
    color: #a078dc;
    text-transform: uppercase;
    vertical-align: middle;
    margin-left: 4px;
  }

  .source-badge.nettrack {
    background: rgba(80, 160, 220, 0.15);
    color: #5090d0;
  }

  .source-badge.internal {
    background: rgba(140, 140, 140, 0.15);
    color: #999;
  }

  .empty-sm {
    text-align: center;
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 1rem;
  }

  /* Pagination */
  .pagination {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 0 0;
    gap: 12px;
  }

  .page-size {
    display: flex;
    gap: 2px;
  }

  .page-size-btn {
    background: none;
    border: 1px solid var(--border);
    color: var(--text-dim);
    padding: 2px 8px;
    font-size: 0.7rem;
    cursor: pointer;
    border-radius: 4px;
  }

  .page-size-btn.active {
    background: var(--accent);
    color: var(--bg);
    border-color: var(--accent);
  }

  .page-nav {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .page-btn {
    background: none;
    border: 1px solid var(--border);
    color: var(--text-dim);
    padding: 2px 8px;
    font-size: 0.75rem;
    cursor: pointer;
    border-radius: 4px;
  }

  .page-btn:disabled {
    opacity: 0.3;
    cursor: default;
  }

  .page-btn:not(:disabled):hover {
    border-color: var(--accent);
    color: var(--accent);
  }

  .page-info {
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  /* Modal form */
  .modal-form {
    margin-top: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .modal-form label {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 0.85rem;
    color: var(--text-dim);
  }

  .modal-form input,
  .modal-form select {
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 8px 10px;
    font-size: 0.85rem;
  }

  .modal-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 0.5rem;
  }
</style>
