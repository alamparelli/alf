<script lang="ts">
  import { onMount } from 'svelte'
  import { Plus, Trash2, RefreshCw, ShieldOff, Shield } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import { api } from '../lib/api'
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
  }

  let config = $state<FirewallConfig>({ mode: 'log-only', port: 4751, rules: [] })
  let log = $state<RequestEntry[]>([])
  let autoRefresh = $state(true)
  let loading = $state(false)

  // Add rule modal
  let showAddRule = $state(false)
  let newPattern = $state('')
  let newAction = $state('allow')

  let refreshTimer: ReturnType<typeof setInterval> | undefined

  async function loadFirewall() {
    loading = true
    try {
      const data = await api<{ config: FirewallConfig; log: RequestEntry[] }>('/api/firewall')
      config = data.config || { mode: 'log-only', port: 4751, rules: [] }
      log = data.log || []
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
    // Check if rule already exists
    if (config.rules.some(r => r.pattern === host)) {
      toasts.show('Rule already exists for ' + host, 'info')
      return
    }
    config.rules = [...config.rules, { pattern: host, action: 'deny' }]
    saveConfig()
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

  $effect(() => {
    if (autoRefresh) {
      refreshTimer = setInterval(loadFirewall, 3000)
    } else {
      if (refreshTimer) clearInterval(refreshTimer)
      refreshTimer = undefined
    }
    return () => {
      if (refreshTimer) clearInterval(refreshTimer)
    }
  })

  onMount(() => {
    loadFirewall()
  })
</script>

<div class="firewall-view">
  <h2>Firewall</h2>

  <!-- Mode toggle -->
  <Card>
    <div class="mode-section">
      <h3>Mode</h3>
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

  <!-- Request log -->
  <Card>
    <div class="log-header">
      <h3>Request Log</h3>
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

    {#if log.length > 0}
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
            {#each log as entry}
              <tr class:blocked={entry.blocked}>
                <td class="mono">{formatTime(entry.time)}</td>
                <td><span class="method-badge">{entry.method}</span></td>
                <td class="mono">{entry.host}</td>
                <td class="mono path-cell">{entry.path}</td>
                <td>
                  <span class="status-badge" class:status-ok={entry.status >= 200 && entry.status < 400} class:status-blocked={entry.blocked}>
                    {entry.blocked ? 'DENIED' : entry.status || 'OK'}
                  </span>
                </td>
                <td>
                  {#if !entry.blocked}
                    <button class="btn-icon-sm" onclick={() => addDenyRule(entry.host)} title="Deny this host">
                      <Shield size={12} />
                    </button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <p class="empty-sm">{loading ? 'Loading...' : 'No requests logged'}</p>
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

  .btn-icon-sm:hover {
    color: var(--red, #e55);
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

  .empty-sm {
    text-align: center;
    color: var(--text-dim);
    font-size: 0.85rem;
    padding: 1rem;
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
