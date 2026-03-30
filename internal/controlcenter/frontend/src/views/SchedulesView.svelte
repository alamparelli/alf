<script>
  import { onMount } from 'svelte';
  import { api, esc } from '../lib/api';
  import { toasts } from '../stores/toast.svelte';
  import { events } from '../stores/events.svelte';
  import Card from '../components/shared/Card.svelte';
  import Modal from '../components/shared/Modal.svelte';
  import { Plus, Play, Trash2, Search, ChevronDown, ChevronRight, ChevronsUpDown, Clock, Power, PowerOff, Settings, Pencil } from 'lucide-svelte';

  // --- State ---
  let jobs = $state([]);
  let loading = $state(true);
  let searchQuery = $state('');
  let activeFilter = $state('all');
  let expandedIds = $state(new Set());
  let allExpanded = $state(false);
  let runHistories = $state({});

  // Modal
  let showModal = $state(false);
  let editingJob = $state(null);
  let form = $state({
    name: '', description: '', reason: '', schedule: '', prompt: '', tier: '', output: 'chat',
    command: '', message: '', timeout: '', skills: [],
  });

  // Tiers for dropdown
  let tierNames = $state([]);

  // SSE
  let unsubEvents;

  const FILTERS = [
    { key: 'all', label: 'All' },
    { key: 'recurring', label: 'Recurring' },
    { key: 'today', label: 'Today' },
    { key: 'week', label: 'This Week' },
    { key: 'later', label: 'Later' },
    { key: 'oneshot', label: 'One-shot' },
    { key: 'managed', label: 'Managed' },
    { key: 'obsolete', label: 'Obsolete' },
  ];

  // --- Derived ---
  let filteredJobs = $derived((() => {
    let list = jobs;

    // Search
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase();
      list = list.filter(j =>
        j.name.toLowerCase().includes(q) ||
        (j.prompt || '').toLowerCase().includes(q) ||
        (j.schedule || '').toLowerCase().includes(q)
      );
    }

    // Filter
    const now = new Date();
    const todayEnd = new Date(now); todayEnd.setHours(23, 59, 59, 999);
    const weekEnd = new Date(now); weekEnd.setDate(weekEnd.getDate() + 7);

    switch (activeFilter) {
      case 'recurring':
        list = list.filter(j => isCron(j.schedule));
        break;
      case 'today':
        list = list.filter(j => {
          if (isCron(j.schedule)) return false;
          const d = new Date(j.next_run || j.schedule);
          return d >= now && d <= todayEnd;
        });
        break;
      case 'week':
        list = list.filter(j => {
          if (isCron(j.schedule)) return false;
          const d = new Date(j.next_run || j.schedule);
          return d >= now && d <= weekEnd;
        });
        break;
      case 'later':
        list = list.filter(j => {
          if (isCron(j.schedule)) return false;
          const d = new Date(j.next_run || j.schedule);
          return d > weekEnd;
        });
        break;
      case 'oneshot':
        list = list.filter(j => !isCron(j.schedule));
        break;
      case 'managed':
        list = list.filter(j => j.managed);
        break;
      case 'obsolete':
        list = list.filter(j => {
          if (isCron(j.schedule)) return false;
          const d = new Date(j.next_run || j.schedule);
          return d < now;
        });
        break;
    }

    return list;
  })());

  // --- Helpers ---
  function isCron(schedule) {
    if (!schedule) return false;
    // Cron has 5-6 space-separated fields; ISO dates don't
    return schedule.trim().split(/\s+/).length >= 5;
  }

  function relativeTime(dateStr) {
    if (!dateStr) return '--';
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return dateStr;
    const now = Date.now();
    const diff = d.getTime() - now;
    const absDiff = Math.abs(diff);
    const past = diff < 0;

    if (absDiff < 60000) return past ? 'just now' : 'in a moment';
    if (absDiff < 3600000) {
      const m = Math.round(absDiff / 60000);
      return past ? `${m}m ago` : `in ${m}m`;
    }
    if (absDiff < 86400000) {
      const h = Math.round(absDiff / 3600000);
      return past ? `${h}h ago` : `in ${h}h`;
    }
    const days = Math.round(absDiff / 86400000);
    return past ? `${days}d ago` : `in ${days}d`;
  }

  function statusColor(status) {
    switch (status) {
      case 'ok': return 'var(--green)';
      case 'error': return 'var(--red)';
      case 'timeout': return 'var(--yellow)';
      case 'skipped': return 'var(--text-dim)';
      default: return 'var(--text-dim)';
    }
  }

  // --- Load ---
  async function loadJobs() {
    loading = true;
    try {
      const data = await api('GET', '/api/schedules');
      jobs = data.jobs || [];
    } catch (e) {
      toasts.error('Failed to load schedules: ' + (e.error || e.message));
    } finally {
      loading = false;
    }
  }

  async function loadTierNames() {
    try {
      const data = await api('GET', '/api/tiers');
      tierNames = (data.tiers || []).map(t => t.name);
    } catch {}
  }

  async function loadHistory(jobId) {
    try {
      const data = await api('GET', `/api/schedules/logs?id=${esc(jobId)}&limit=5`);
      runHistories[jobId] = { records: data.records || [], stats: data.stats || {} };
      runHistories = { ...runHistories };
    } catch {}
  }

  // --- Actions ---
  async function runNow(id) {
    try {
      await api('POST', '/api/schedules/run', { id });
      toasts.success('Job triggered');
    } catch (e) {
      toasts.error('Run failed: ' + (e.error || e.message));
    }
  }

  async function deleteJob(id, name) {
    if (!confirm(`Delete schedule "${name}"?`)) return;
    try {
      await api('DELETE', `/api/schedules?id=${esc(id)}`);
      toasts.success('Deleted');
      await loadJobs();
    } catch (e) {
      toasts.error('Delete failed: ' + (e.error || e.message));
    }
  }

  async function toggleEnabled(job) {
    try {
      await api('PUT', '/api/schedules', { id: job.id, fields: { enabled: job.enabled ? 'false' : 'true' } });
      await loadJobs();
    } catch (e) {
      toasts.error('Toggle failed: ' + (e.error || e.message));
    }
  }

  // --- Expand/Collapse ---
  function toggleExpand(id) {
    if (expandedIds.has(id)) {
      expandedIds.delete(id);
    } else {
      expandedIds.add(id);
      if (!runHistories[id]) loadHistory(id);
    }
    expandedIds = new Set(expandedIds);
  }

  function toggleAllExpanded() {
    allExpanded = !allExpanded;
    if (allExpanded) {
      expandedIds = new Set(filteredJobs.map(j => j.id));
      for (const j of filteredJobs) {
        if (!runHistories[j.id]) loadHistory(j.id);
      }
    } else {
      expandedIds = new Set();
    }
  }

  // --- Modal ---
  function openAddModal() {
    editingJob = null;
    form = { name: '', description: '', reason: '', schedule: '', prompt: '', tier: '', output: 'chat', command: '', message: '', timeout: '', skills: [] };
    showModal = true;
  }

  function openEditModal(job) {
    editingJob = job;
    form = {
      name: job.name,
      description: job.description || '',
      reason: job.reason || '',
      schedule: job.schedule,
      prompt: job.prompt || '',
      tier: job.tier || '',
      output: job.output || 'chat',
      command: job.command || '',
      message: job.message || '',
      timeout: job.timeout || '',
      skills: [...(job.skills || [])],
    };
    showModal = true;
  }

  async function saveForm() {
    if (!form.name.trim() || !form.schedule.trim()) {
      toasts.error('Name and schedule are required');
      return;
    }

    try {
      if (editingJob) {
        const fields = {};
        if (form.name !== editingJob.name) fields.name = form.name;
        if (form.schedule !== editingJob.schedule) fields.schedule = form.schedule;
        if (form.prompt !== (editingJob.prompt || '')) fields.prompt = form.prompt;
        if (form.tier !== (editingJob.tier || '')) fields.tier = form.tier;
        if (form.output !== (editingJob.output || 'chat')) fields.output = form.output;
        if (form.command !== (editingJob.command || '')) fields.command = form.command;
        if (form.message !== (editingJob.message || '')) fields.message = form.message;
        if (form.reason !== (editingJob.reason || '')) fields.reason = form.reason;
        if (form.description !== (editingJob.description || '')) fields.description = form.description;
        if (form.timeout !== (editingJob.timeout || '')) fields.timeout = form.timeout;

        if (Object.keys(fields).length > 0) {
          await api('PUT', '/api/schedules', { id: editingJob.id, fields });
        }
      } else {
        await api('POST', '/api/schedules', {
          name: form.name,
          schedule: form.schedule,
          prompt: form.prompt,
          tier: form.tier,
          output: form.output,
          command: form.command || undefined,
          message: form.message || undefined,
          reason: form.reason || undefined,
          timeout: form.timeout || undefined,
          skills: form.skills.length ? form.skills : undefined,
        });
      }
      showModal = false;
      toasts.success(editingJob ? 'Job updated' : 'Job created');
      await loadJobs();
    } catch (e) {
      toasts.error('Save failed: ' + (e.error || e.message));
    }
  }

  onMount(() => {
    loadJobs();
    loadTierNames();
    unsubEvents = events.subscribe('schedules', loadJobs);
    return () => { unsubEvents?.(); };
  });
</script>

<div class="view-schedules">
  <h2>Schedules</h2>
  <div class="toolbar">
    <div class="search-box">
      <Search size={14} />
      <input type="text" placeholder="Search..." bind:value={searchQuery} />
    </div>
    <button class="btn btn-ghost btn-sm" onclick={toggleAllExpanded} title="Expand/collapse all">
      <ChevronsUpDown size={14} />
    </button>
    <button class="btn btn-primary btn-sm" onclick={openAddModal}>
      <Plus size={14} /> Add Job
    </button>
  </div>

  <div class="filter-tabs">
    {#each FILTERS as f}
      <button
        class="tab"
        class:active={activeFilter === f.key}
        onclick={() => activeFilter = f.key}
      >
        {f.label}
      </button>
    {/each}
  </div>

  {#if loading}
    <div class="loading">Loading schedules...</div>
  {:else if filteredJobs.length === 0}
    <div class="empty">No schedules match the current filter.</div>
  {:else}
    <div class="job-list">
      {#each filteredJobs as job (job.id)}
        <Card>
          <div class="job-card">
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div class="job-header" onclick={() => toggleExpand(job.id)}>
              <span class="expand-icon">
                {#if expandedIds.has(job.id)}
                  <ChevronDown size={14} />
                {:else}
                  <ChevronRight size={14} />
                {/if}
              </span>
              <div class="job-title">
                <strong>{job.name}</strong>
                {#if job.running}
                  <span class="badge badge-running">running</span>
                {/if}
                {#if !job.enabled}
                  <span class="badge badge-disabled">disabled</span>
                {/if}
                {#if job.managed}
                  <span class="badge badge-managed">managed</span>
                {/if}
                {#if job.system}
                  <span class="badge badge-system">system</span>
                {/if}
                {#if job.description}
                  <span class="job-description">{job.description}</span>
                {/if}
                {#if job.reason}
                  <span class="job-reason">{job.reason}</span>
                {/if}
              </div>
              <div class="job-meta">
                <span class="meta-item"><Clock size={12} /> {job.schedule}</span>
                {#if job.tier}
                  <span class="badge badge-tier">{job.tier}</span>
                {/if}
                {#if job.output}
                  <span class="meta-item">{job.output}</span>
                {/if}
              </div>
            </div>

            <div class="job-controls">
              <div class="job-times">
                <span>Last: {relativeTime(job.last_run)}</span>
                <span>Next: {relativeTime(job.next_run)}</span>
                {#if job.last_error}
                  <span class="error-text" title={job.last_error}>Error: {job.last_error.slice(0, 60)}</span>
                {/if}
              </div>
              <div class="job-actions">
              <button class="btn btn-ghost btn-sm" onclick={() => runNow(job.id)} title="Run now">
                <Play size={12} />
              </button>
              <button class="btn btn-ghost btn-sm" onclick={() => toggleEnabled(job)} title={job.enabled ? 'Disable' : 'Enable'}>
                {#if job.enabled}
                  <PowerOff size={12} />
                {:else}
                  <Power size={12} />
                {/if}
              </button>
              <button class="btn btn-ghost btn-sm" onclick={() => openEditModal(job)} title="Edit">
                <Pencil size={12} />
              </button>
              {#if !job.system}
                <button class="btn btn-ghost btn-sm btn-danger" onclick={() => deleteJob(job.id, job.name)} title="Delete">
                  <Trash2 size={12} />
                </button>
              {/if}
              </div>
            </div>

            {#if expandedIds.has(job.id)}
              <div class="job-body">
                {#if job.prompt}
                  <div class="prompt-preview">
                    <strong>Prompt:</strong>
                    <pre>{job.prompt.length > 300 ? job.prompt.slice(0, 300) + '...' : job.prompt}</pre>
                  </div>
                {/if}
                {#if job.message}
                  <div class="prompt-preview">
                    <strong>Message:</strong>
                    <pre>{job.message}</pre>
                  </div>
                {/if}
                {#if job.command}
                  <div class="prompt-preview">
                    <strong>Command:</strong>
                    <pre>{job.command}</pre>
                  </div>
                {/if}

                {#if runHistories[job.id]}
                  {@const hist = runHistories[job.id]}
                  <div class="run-history">
                    <strong>Run History</strong>
                    {#if hist.stats?.total_runs}
                      <span class="stats-line">
                        {hist.stats.total_runs} runs | {hist.stats.ok_count} ok | {hist.stats.fail_count} fail
                        {#if hist.stats.total_cost_usd}
                          | ${hist.stats.total_cost_usd.toFixed(4)}
                        {/if}
                      </span>
                    {/if}
                    {#if hist.records.length > 0}
                      <div class="run-list">
                        {#each hist.records as rec}
                          <div class="run-entry">
                            <span class="run-status" style="color:{statusColor(rec.status)}">{rec.status}</span>
                            <span>{relativeTime(rec.started_at)}</span>
                            <span>{(rec.duration_ms / 1000).toFixed(1)}s</span>
                            {#if rec.model}<span class="run-model">{rec.model}</span>{/if}
                            {#if rec.error}<span class="error-text" title={rec.error}>{rec.error.slice(0, 40)}</span>{/if}
                          </div>
                        {/each}
                      </div>
                    {:else}
                      <span class="muted">No runs yet.</span>
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<!-- Add/Edit Modal -->
<Modal open={showModal} onclose={() => showModal = false}>
  <h3>{editingJob ? 'Edit' : 'Add'} Schedule</h3>
      <div class="form-grid">
        <label class="full-width">
          Name
          <input type="text" bind:value={form.name} placeholder="e.g. daily-report" />
        </label>
        <label class="full-width">
          Description
          <input type="text" bind:value={form.description} placeholder="Short description of what this job does" />
        </label>
        <label class="full-width">
          Schedule (cron or ISO date)
          <input type="text" bind:value={form.schedule} placeholder="0 9 * * * or 2026-03-25T10:00:00Z" />
        </label>
        <label class="full-width">
          Reason
          <input type="text" bind:value={form.reason} placeholder="Why does this job exist? (context for the LLM)" />
        </label>
        <label class="full-width">
          Prompt
          <textarea bind:value={form.prompt} rows="4" placeholder="What should the agent do?"></textarea>
        </label>
        <label>
          Tier
          <select bind:value={form.tier}>
            <option value="">default</option>
            {#each tierNames as t}
              <option value={t}>{t}</option>
            {/each}
          </select>
        </label>
        <label>
          Output Mode
          <select bind:value={form.output}>
            <option value="chat">Chat (TG + CC)</option>
            <option value="tg">Telegram only</option>
            <option value="cc">Control Center only</option>
            <option value="file">File</option>
            <option value="both">Chat + File</option>
            <option value="silent">Silent</option>
          </select>
        </label>
        <label>
          Command (optional)
          <input type="text" bind:value={form.command} placeholder="Shell command" />
        </label>
        <label>
          Timeout (optional)
          <input type="text" bind:value={form.timeout} placeholder="e.g. 5m, 1h" />
        </label>
        <label class="full-width">
          Message (reminder mode - no prompt/tier)
          <textarea bind:value={form.message} rows="2" placeholder="Direct notification text"></textarea>
        </label>
      </div>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showModal = false}>Cancel</button>
    <button class="btn btn-primary" onclick={saveForm}>
      {editingJob ? 'Update' : 'Create'}
    </button>
  </div>
</Modal>

<style>
  .view-schedules {
    width: 100%;
    padding: 8px 0;
  }
  .view-schedules h2 {
    margin-bottom: 16px;
  }
  .loading, .empty {
    text-align: center;
    padding: 3rem;
    color: var(--text-dim);
  }
  .job-list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .job-card {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
  }
  .job-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    cursor: pointer;
    flex-wrap: wrap;
  }
  .expand-icon {
    flex-shrink: 0;
    color: var(--text-dim);
  }
  .job-title {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex: 1;
    min-width: 0;
    flex-wrap: wrap;
  }

  .job-description {
    width: 100%;
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    font-weight: normal;
  }

  .job-reason {
    width: 100%;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    font-style: italic;
    font-weight: normal;
  }

  .job-meta {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }
  .meta-item {
    display: flex;
    align-items: center;
    gap: 0.2rem;
  }
  .badge-tier { background: var(--sapphire); color: #fff; }
  .badge-running { background: var(--green); color: #fff; animation: pulse 1.5s infinite; }
  .badge-disabled { background: var(--yellow); color: #fff; }
  .badge-managed { background: var(--mauve); color: #fff; }
  .badge-system { background: var(--text-dim); color: #fff; }
  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.6; }
  }
  .job-controls {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-left: 1.8rem;
  }
  .job-times {
    display: flex;
    gap: 1rem;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }
  .error-text {
    color: var(--red);
    font-size: var(--font-xs, 11px);
  }
  .job-actions {
    display: flex;
    gap: 0.2rem;
  }
  .job-body {
    padding: 0.5rem 0 0 1.8rem;
    border-top: 1px solid var(--border);
    margin-top: 0.3rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .prompt-preview pre {
    font-size: var(--font-xs, 11px);
    background: var(--bg);
    padding: 0.4rem;
    border-radius: 4px;
    white-space: pre-wrap;
    word-break: break-word;
    margin: 0.2rem 0 0;
  }
  .run-history {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .stats-line {
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }
  .run-list {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .run-entry {
    display: flex;
    gap: 0.6rem;
    font-size: var(--font-xs, 11px);
    align-items: center;
  }
  .run-status {
    font-weight: 600;
    min-width: 50px;
  }
  .run-model {
    color: var(--text-dim);
  }
  .muted {
    color: var(--text-dim);
    font-size: var(--font-xs, 11px);
  }

  /* Modal content styles */
  .form-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.8rem;
  }
  .form-grid label {
    display: flex;
    flex-direction: column;
    gap: 0.2rem;
    font-size: var(--font-sm, 13px);
  }
  .form-grid input, .form-grid select, .form-grid textarea {
    padding: 0.4rem;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg);
    color: var(--text);
    font-size: var(--font-sm, 13px);
  }
  .full-width {
    grid-column: 1 / -1;
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 1rem;
  }
</style>
