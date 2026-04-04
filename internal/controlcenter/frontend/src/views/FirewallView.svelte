<script lang="ts">
  import { onMount } from 'svelte'
  import { Plus, Trash2, RefreshCw, ShieldOff, Shield } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import Toggle from '../components/shared/Toggle.svelte'
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

<div class="view">
  <h2>Firewall</h2>

  <!-- Mode toggle + Kill switch -->
  <Card>
    <div class="flex items-center justify-between">
      <h3>Mode</h3>
      <div class="flex items-center gap-sm">
        <div class="filter-tabs">
          <button
            class="tab"
            class:active={config.mode === 'log-only'}
            onclick={() => setMode('log-only')}
          >
            <ShieldOff size={14} /> Log Only
          </button>
          <button
            class="tab"
            class:active={config.mode === 'enforce'}
            onclick={() => setMode('enforce')}
          >
            <Shield size={14} /> Enforce
          </button>
        </div>
        <button
          class="btn-danger-toggle"
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
    <div class="section-header">
      <h3>Rules</h3>
      <button class="btn btn-primary btn-sm" onclick={() => showAddRule = true}>
        <Plus size={14} /> Add Rule
      </button>
    </div>

    {#if config.rules.length > 0}
      <div class="row-list">
        {#each config.rules as rule, i}
          <div class="row-item">
            <code class="text-mono flex-1 text-sm">{rule.pattern}</code>
            <span class="tag" class:tag-success={rule.action === 'allow'} class:tag-danger={rule.action === 'deny'}>
              {rule.action}
            </span>
            <button class="btn-icon-xs" onclick={() => removeRule(i)} title="Delete rule">
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
    <div class="section-header">
      <div class="tab-bar" style="border:none;margin:0;gap:0">
        <button class="tab-item" class:active={activeTab === 'log'} onclick={() => activeTab = 'log'}>Request Log</button>
        <button class="tab-item" class:active={activeTab === 'hosts'} onclick={() => activeTab = 'hosts'}>Hosts ({hosts.length})</button>
      </div>
      <div class="flex items-center gap-sm">
        <Toggle bind:checked={autoRefresh} label="Auto-refresh" />
        <button class="btn btn-ghost btn-sm" onclick={() => loadFirewall()} title="Refresh">
          <RefreshCw size={14} />
        </button>
        <button class="btn btn-ghost btn-sm" onclick={clearLog}>Clear</button>
      </div>
    </div>

    {#if activeTab === 'log'}
      <div class="filter-bar">
        <input type="text" class="input text-mono" placeholder="Filter by host, method, source..." bind:value={logFilter} />
        {#if logFilter}<span class="filter-count">{filteredLog.length} / {log.length}</span>{/if}
      </div>
      {#if filteredLog.length > 0}
        <div class="table-wrap">
          <table class="data-table">
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
                <tr class:row-danger={entry.blocked}>
                  <td class="text-mono">{formatTime(entry.time)}</td>
                  <td><span class="badge-inline badge-inline-accent">{entry.method}</span></td>
                  <td class="text-mono">{entry.host}{#if entry.source} <span class="badge-inline badge-inline-mauve">{entry.source}</span>{/if}</td>
                  <td class="text-mono truncate" style="max-width:200px">{entry.path}</td>
                  <td>
                    <span class="badge-inline" class:badge-inline-success={entry.status >= 200 && entry.status < 400} class:badge-inline-danger={entry.blocked}>
                      {entry.blocked ? 'DENIED' : entry.status || 'OK'}
                    </span>
                  </td>
                  <td class="flex gap-xs">
                    <button class="btn-icon-xs hover-success" onclick={() => addAllowRule(entry.host)} title="Allow this host">
                      <ShieldOff size={12} />
                    </button>
                    <button class="btn-icon-xs hover-danger" onclick={() => addDenyRule(entry.host)} title="Deny this host">
                      <Shield size={12} />
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <div class="pagination-bar">
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
        <input type="text" class="input text-mono" placeholder="Filter by host..." bind:value={hostsFilter} />
        {#if hostsFilter}<span class="filter-count">{filteredHosts.length} / {hosts.length}</span>{/if}
      </div>
      {#if filteredHosts.length > 0}
        <div class="table-wrap">
          <table class="data-table">
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
                <tr class:row-danger={h.blocked > 0 && h.allowed === 0}>
                  <td class="text-mono">{h.host}{#if h.vault} <span class="badge-inline badge-inline-mauve">vault</span>{/if}</td>
                  <td>{h.count}</td>
                  <td>{h.allowed}</td>
                  <td>{h.blocked || ''}</td>
                  <td class="text-mono">{formatDateTime(h.last_seen)}</td>
                  <td class="flex gap-xs">
                    <button class="btn-icon-xs hover-success" onclick={() => addAllowRule(h.host)} title="Allow this host">
                      <ShieldOff size={12} />
                    </button>
                    <button class="btn-icon-xs hover-danger" onclick={() => addDenyRule(h.host)} title="Deny this host">
                      <Shield size={12} />
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <div class="pagination-bar">
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
      <label class="form-group">
        Pattern
        <input class="input" type="text" bind:value={newPattern} placeholder="*.example.com" />
      </label>
      <label class="form-group">
        Action
        <select class="select" bind:value={newAction}>
          <option value="allow">Allow</option>
          <option value="deny">Deny</option>
        </select>
      </label>
      <div class="modal-actions">
        <button class="btn btn-primary" onclick={addRule}>Add</button>
        <button class="btn btn-ghost" onclick={() => showAddRule = false}>Cancel</button>
      </div>
    </div>
  </Modal>
</div>

<!-- No scoped CSS — all styles from alf-ui.css -->
