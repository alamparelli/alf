<script lang="ts">
  import { onMount } from 'svelte'
  import { RefreshCw, Search, X } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'

  let availableFiles = $state<string[]>([])
  let selectedFile = $state('')
  let lineCount = $state(200)
  let autoRefresh = $state(true)
  let searchQuery = $state('')
  let lines = $state<string[]>([])
  let loading = $state(false)

  // Filter chips extracted from log content
  let sessions = $state<string[]>([])
  let models = $state<string[]>([])
  let hasErrors = $state(false)
  let activeChip = $state('')

  let logContainer: HTMLPreElement | undefined = $state()
  let refreshTimer: ReturnType<typeof setInterval> | undefined

  async function loadAvailable() {
    try {
      const data = await api<{ available: string[] }>('/api/logs')
      availableFiles = data.available || []
      if (availableFiles.length > 0 && !selectedFile) {
        selectedFile = availableFiles[0]
        await loadLogs()
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load log files', 'error')
    }
  }

  async function loadLogs() {
    if (!selectedFile) return
    loading = true
    try {
      const data = await api<{ lines: string[]; count: number }>(
        `/api/logs?name=${encodeURIComponent(selectedFile)}&n=${lineCount}`
      )
      const wasAtBottom = logContainer
        ? logContainer.scrollTop + logContainer.clientHeight >= logContainer.scrollHeight - 20
        : true

      lines = data.lines || []
      extractChips(lines)

      // Preserve scroll / auto-follow
      if (wasAtBottom && logContainer) {
        requestAnimationFrame(() => {
          if (logContainer) logContainer.scrollTop = logContainer.scrollHeight
        })
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load logs', 'error')
    } finally {
      loading = false
    }
  }

  function extractChips(logLines: string[]) {
    const sessionSet = new Set<string>()
    const modelSet = new Set<string>()
    let foundError = false

    for (const line of logLines) {
      const sessionMatch = line.match(/session[=:]\s*([a-zA-Z0-9_-]+)/i)
      if (sessionMatch) sessionSet.add(sessionMatch[1])

      const modelMatch = line.match(/model[=:]\s*([a-zA-Z0-9._-]+)/i)
      if (modelMatch) modelSet.add(modelMatch[1])

      if (/\b(ERROR|FATAL|PANIC)\b/i.test(line)) foundError = true
    }

    sessions = [...sessionSet].slice(0, 10)
    models = [...modelSet].slice(0, 10)
    hasErrors = foundError
  }

  function lineClass(line: string): string {
    if (/\b(ERROR|FATAL|PANIC)\b/i.test(line)) return 'log-error'
    if (/\bWARN/i.test(line)) return 'log-warn'
    if (/\bDEBUG\b/i.test(line)) return 'log-debug'
    if (/\b(response|→)\b/i.test(line)) return 'log-accent'
    return ''
  }

  let filteredLines = $derived.by(() => {
    let result = lines
    if (searchQuery) {
      const q = searchQuery.toLowerCase()
      result = result.filter(l => l.toLowerCase().includes(q))
    }
    if (activeChip) {
      result = result.filter(l => l.includes(activeChip))
    }
    return result
  })

  function toggleChip(value: string) {
    activeChip = activeChip === value ? '' : value
  }

  $effect(() => {
    if (autoRefresh) {
      refreshTimer = setInterval(loadLogs, 5000)
    } else {
      if (refreshTimer) clearInterval(refreshTimer)
      refreshTimer = undefined
    }
    return () => {
      if (refreshTimer) clearInterval(refreshTimer)
    }
  })

  onMount(() => {
    loadAvailable()
  })
</script>

<div class="logs-view">
  <h2>Logs</h2>

  <Card>
    <div class="controls">
      <select bind:value={selectedFile} onchange={() => loadLogs()}>
        {#each availableFiles as f}
          <option value={f}>{f}</option>
        {/each}
      </select>

      <select bind:value={lineCount} onchange={() => loadLogs()}>
        <option value={100}>100 lines</option>
        <option value={200}>200 lines</option>
        <option value={500}>500 lines</option>
        <option value={1000}>1000 lines</option>
      </select>

      <label class="auto-refresh">
        <input type="checkbox" bind:checked={autoRefresh} />
        Auto-refresh
      </label>

      <button class="btn-icon" onclick={() => loadLogs()} title="Refresh">
        <RefreshCw size={16} />
      </button>
    </div>

    <div class="search-row">
      <div class="search-input">
        <Search size={14} />
        <input type="text" placeholder="Filter logs..." bind:value={searchQuery} />
        {#if searchQuery}
          <button class="btn-clear" onclick={() => searchQuery = ''}>
            <X size={14} />
          </button>
        {/if}
      </div>
    </div>

    {#if sessions.length > 0 || models.length > 0 || hasErrors}
      <div class="chips">
        {#each sessions as s}
          <button
            class="chip"
            class:active={activeChip === s}
            onclick={() => toggleChip(s)}
          >session:{s}</button>
        {/each}
        {#each models as m}
          <button
            class="chip"
            class:active={activeChip === m}
            onclick={() => toggleChip(m)}
          >model:{m}</button>
        {/each}
        {#if hasErrors}
          <button
            class="chip chip-error"
            class:active={activeChip === 'ERROR'}
            onclick={() => toggleChip('ERROR')}
          >errors</button>
        {/if}
      </div>
    {/if}
  </Card>

  <pre class="log-output" bind:this={logContainer}>{#each filteredLines as line}<span class={lineClass(line)}>{line}
</span>{/each}{#if filteredLines.length === 0}{#if loading}<span class="log-debug">Loading...</span>{:else}<span class="log-debug">No log lines</span>{/if}{/if}</pre>
</div>

<style>
  .logs-view {
    padding: 8px 0;
    width: 100%;
  }

  h2 {
    margin-bottom: 16px;
  }

  .controls {
    display: flex;
    gap: 0.75rem;
    align-items: center;
    flex-wrap: wrap;
  }

  select {
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px 10px;
    font-size: 0.85rem;
  }

  .auto-refresh {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 0.85rem;
    color: var(--text-dim);
    cursor: pointer;
  }

  .btn-icon {
    background: none;
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    padding: 6px;
    cursor: pointer;
    display: flex;
    align-items: center;
  }

  .btn-icon:hover {
    color: var(--accent);
    border-color: var(--accent);
  }

  .search-row {
    margin-top: 0.75rem;
  }

  .search-input {
    display: flex;
    align-items: center;
    gap: 6px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px 10px;
  }

  .search-input input {
    flex: 1;
    background: none;
    border: none;
    color: var(--text);
    outline: none;
    font-size: 0.85rem;
  }

  .btn-clear {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    display: flex;
    padding: 2px;
  }

  .chips {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-top: 0.75rem;
  }

  .chip {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 2px 10px;
    font-size: 0.75rem;
    color: var(--text-dim);
    cursor: pointer;
  }

  .chip:hover, .chip.active {
    border-color: var(--accent);
    color: var(--accent);
  }

  .chip-error {
    border-color: var(--red, #e55);
    color: var(--red, #e55);
  }

  .log-output {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1rem;
    font-family: 'JetBrains Mono', 'Fira Code', monospace;
    font-size: 0.8rem;
    line-height: 1.5;
    max-height: 70vh;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-all;
    margin-top: 1rem;
  }

  .log-output span {
    display: block;
  }

  :global(.log-error) {
    color: var(--red, #e55);
  }

  :global(.log-warn) {
    color: var(--yellow, #eb6);
  }

  :global(.log-debug) {
    opacity: 0.5;
  }

  :global(.log-accent) {
    color: var(--accent);
  }
</style>
