<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { Play, Square, Trash2, ChevronDown, ChevronRight, RefreshCw, CheckCircle, XCircle, Clock, AlertTriangle, Users, Loader2 } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { events } from '../stores/events.svelte'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'

  interface AgentCall {
    agent: string
    task?: string
    status: string
    text?: string
    error?: string
    cost_usd: number
  }

  interface TaskMeta {
    id: string
    prompt?: string
    response?: string
    started_at: string
    completed_at?: string
    iterations: number
    total_cost_usd: number
    agent_calls: AgentCall[]
    status: string
    plan?: { step: number; description: string; agents?: string[] }[]
    questions?: string[]
    need_validation?: boolean
    validation_feedback?: string
    source?: string
    team?: string
  }

  // Teams
  let teams = $state<{ name: string; description?: string }[]>([])
  let selectedTeam = $state('')

  // Launcher
  let prompt = $state('')
  let needValidation = $state(false)
  let launching = $state(false)

  // Tasks
  let running = $state<TaskMeta[]>([])
  let completed = $state<TaskMeta[]>([])
  let expandedTasks = $state<Record<string, boolean>>({})
  let showCompleted = $state(false)
  let autoRefresh = $state(true)
  let refreshTimer: ReturnType<typeof setInterval> | undefined

  // Expandable agent outputs
  let expandedOutputs = $state<Record<string, boolean>>({})

  function toggleOutput(key: string) {
    expandedOutputs[key] = !expandedOutputs[key]
  }

  // Approval
  let approvalFeedback = $state<Record<string, string>>({})

  // Track completed task IDs for desktop notifications
  let knownCompletedIds = $state<Set<string>>(new Set())

  function elapsed(startedAt: string): string {
    const ms = Date.now() - new Date(startedAt).getTime()
    const s = Math.floor(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    if (m < 60) return `${m}m ${s % 60}s`
    const h = Math.floor(m / 60)
    return `${h}h ${m % 60}m`
  }

  function statusBadgeClass(status: string): string {
    switch (status) {
      case 'running': return 'badge-running'
      case 'completed': return 'badge-completed'
      case 'failed': case 'timeout': return 'badge-failed'
      case 'awaiting_approval': case 'awaiting_arbitration': return 'badge-waiting'
      case 'interrupted': return 'badge-interrupted'
      default: return ''
    }
  }

  function renderMarkdown(text: string): string {
    if (!text) return ''
    return DOMPurify.sanitize(marked.parse(text, { breaks: true }) as string)
  }

  async function loadTeams() {
    try {
      const data = await api<{ teams: any[] }>('/api/teams')
      teams = data.teams || []
    } catch { /* silent */ }
  }

  async function loadTasks() {
    try {
      const data = await api<{ running: TaskMeta[]; completed: TaskMeta[] }>('/api/tasks')
      const prevRunning = new Set(running.map(t => t.id))
      running = data.running || []
      completed = data.completed || []

      // Desktop notification for newly completed tasks
      for (const t of completed) {
        if (prevRunning.has(t.id) && !knownCompletedIds.has(t.id)) {
          knownCompletedIds.add(t.id)
          notifyCompletion(t)
        }
      }
    } catch { /* silent */ }
  }

  function notifyCompletion(task: TaskMeta) {
    if (!('Notification' in window) || Notification.permission !== 'granted') return
    const label = task.prompt ? (task.prompt.length > 60 ? task.prompt.slice(0, 57) + '...' : task.prompt) : 'Agent task'
    new Notification('Task ' + task.status, { body: label, icon: '/static/favicon.svg' })
  }

  async function launch() {
    if (!prompt.trim()) return
    launching = true
    try {
      await api('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          message: prompt.trim(),
          need_validation: needValidation,
          ...(selectedTeam ? { team: selectedTeam } : {})
        })
      })
      toasts.show('Task launched', 'success')
      prompt = ''
      needValidation = false
      loadTasks()
    } catch (e: any) {
      toasts.show(e.error || e.message || 'Failed to launch task', 'error')
    } finally {
      launching = false
    }
  }

  async function cancelTask(id: string) {
    if (!confirm('Cancel this task?')) return
    try {
      await api(`/api/tasks?id=${encodeURIComponent(id)}`, { method: 'DELETE' })
      toasts.show('Task cancelled', 'success')
      loadTasks()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to cancel', 'error')
    }
  }

  async function deleteTask(id: string) {
    if (!confirm('Delete this completed task?')) return
    try {
      await api(`/api/tasks?id=${encodeURIComponent(id)}&action=delete`, { method: 'DELETE' })
      toasts.show('Task deleted', 'success')
      loadTasks()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to delete', 'error')
    }
  }

  async function approve(id: string, approved: boolean) {
    try {
      await api('/api/tasks/approve', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id, approved, feedback: approvalFeedback[id] || '' })
      })
      toasts.show(approved ? 'Task approved' : 'Task rejected', 'success')
      approvalFeedback[id] = ''
      loadTasks()
    } catch (e: any) {
      toasts.show(e.error || 'Failed', 'error')
    }
  }

  function toggleExpand(id: string) {
    expandedTasks[id] = !expandedTasks[id]
  }

  let unsubEvents: (() => void) | undefined

  // Request notification permission
  function requestNotifPerm() {
    if ('Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission()
    }
  }

  onMount(() => {
    loadTeams()
    loadTasks()
    unsubEvents = events.subscribe('tasks', loadTasks)
    requestNotifPerm()
  })

  onDestroy(() => {
    unsubEvents?.()
  })
</script>

<div class="tasks-view">
  <h2>Tasks</h2>

  <!-- Launcher -->
  <Card>
    <h3>Launch Task</h3>
    <div class="launcher">
      {#if teams.length > 0}
        <div class="form-group">
          <label for="teamSelect">Team</label>
          <select id="teamSelect" class="input" bind:value={selectedTeam}>
            <option value="">Default (single agent)</option>
            {#each teams as team}
              <option value={team.name}>{team.name}</option>
            {/each}
          </select>
        </div>
      {/if}
      <div class="form-group">
        <label for="taskPrompt">Prompt</label>
        <textarea
          id="taskPrompt"
          class="input task-textarea"
          bind:value={prompt}
          placeholder="Describe the task for the agent..."
          rows={3}
        ></textarea>
      </div>
      <div class="launcher-footer">
        <label class="checkbox-label">
          <input type="checkbox" bind:checked={needValidation} />
          Require approval before execution
        </label>
        <button class="btn btn-primary" onclick={launch} disabled={launching || !prompt.trim()}>
          {#if launching}
            <Loader2 size={14} class="spin" /> Launching...
          {:else}
            <Play size={14} /> Launch
          {/if}
        </button>
      </div>
    </div>
  </Card>

  <!-- Controls -->
  <div class="task-controls">
    <button class="btn btn-sm" onclick={loadTasks}>
      <RefreshCw size={13} /> Refresh
    </button>
  </div>

  <!-- Running tasks -->
  {#if running.length > 0}
    <h3>Running ({running.length})</h3>
    {#each running as task (task.id)}
      <Card>
        <div class="task-header" onclick={() => toggleExpand(task.id)}>
          <div class="task-toggle">
            {#if expandedTasks[task.id]}
              <ChevronDown size={16} />
            {:else}
              <ChevronRight size={16} />
            {/if}
          </div>
          <div class="task-info">
            <span class="task-name">{task.prompt ? (task.prompt.length > 80 ? task.prompt.slice(0, 77) + '...' : task.prompt) : 'Agent task'}</span>
            <div class="task-meta">
              <span class="badge {statusBadgeClass(task.status)}">{task.status.replace(/_/g, ' ')}</span>
              {#if task.total_cost_usd > 0}
                <span class="cost-badge">${task.total_cost_usd.toFixed(4)}</span>
              {/if}
              <span class="task-elapsed"><Clock size={12} /> {elapsed(task.started_at)}</span>
              {#if task.team}
                <span class="task-team"><Users size={12} /> {task.team}</span>
              {/if}
              {#if task.iterations > 0}
                <span class="task-iters">{task.iterations} iterations</span>
              {/if}
            </div>
          </div>
          <div class="task-actions">
            <button class="btn btn-sm btn-danger" onclick={(e: MouseEvent) => { e.stopPropagation(); cancelTask(task.id) }} title="Cancel">
              <Square size={13} />
            </button>
          </div>
        </div>

        {#if expandedTasks[task.id]}
          <div class="task-detail">
            {#if task.prompt}
              <div class="task-prompt-box">{task.prompt}</div>
            {/if}

            {#if task.plan && task.plan.length > 0}
              <div class="task-plan">
                <h4>Plan</h4>
                {#each task.plan as step, i}
                  <div class="plan-step">
                    <span class="plan-num">{step.step || i + 1}.</span>
                    <span class="plan-name">{step.description}</span>
                    {#if step.agents && step.agents.length > 0}
                      <span class="plan-agents">{step.agents.join(', ')}</span>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}

            {#if task.agent_calls && task.agent_calls.length > 0}
              <div class="agent-steps">
                <h4>Agent Steps ({task.agent_calls.length})</h4>
                {#each task.agent_calls as call, i}
                  {@const outputKey = `${task.id}-${i}`}
                  {@const isExpanded = expandedOutputs[outputKey]}
                  <div class="agent-step agent-step-{call.status === 'completed' ? 'completed' : call.status === 'failed' || call.status === 'timeout' ? 'failed' : 'working'}">
                    <div class="agent-step-header" onclick={() => toggleOutput(outputKey)} role="button" tabindex="0" onkeydown={(e) => e.key === 'Enter' && toggleOutput(outputKey)}>
                      {#if isExpanded}
                        <ChevronDown size={13} />
                      {:else}
                        <ChevronRight size={13} />
                      {/if}
                      <span class="agent-name">{call.agent}</span>
                      <span class="badge {statusBadgeClass(call.status)}">{call.status}</span>
                      {#if call.cost_usd > 0}
                        <span class="cost">${call.cost_usd.toFixed(4)}</span>
                      {/if}
                    </div>
                    {#if isExpanded}
                      <div class="agent-step-body">
                        {#if call.task}
                          <div class="agent-task markdown-body">{@html renderMarkdown(call.task)}</div>
                        {/if}
                        {#if call.text}
                          <div class="agent-output markdown-body">{@html renderMarkdown(call.text)}</div>
                        {/if}
                        {#if call.error}
                          <div class="agent-error">{call.error}</div>
                        {/if}
                      </div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}

            {#if task.questions && task.questions.length > 0}
              <div class="task-questions">
                <h4>Questions</h4>
                {#each task.questions as q}
                  <p>{q}</p>
                {/each}
              </div>
            {/if}

            <!-- Approval UI -->
            {#if task.status === 'awaiting_approval' || task.status === 'awaiting_arbitration'}
              <div class="approval-ui">
                <h4>{task.status === 'awaiting_approval' ? 'Approval Required' : 'Arbitration Required'}</h4>
                <textarea
                  class="input"
                  placeholder="Optional feedback..."
                  rows={2}
                  bind:value={approvalFeedback[task.id]}
                ></textarea>
                <div class="approval-actions">
                  <button class="btn btn-primary" onclick={() => approve(task.id, true)}>
                    <CheckCircle size={14} /> Approve
                  </button>
                  <button class="btn btn-danger" onclick={() => approve(task.id, false)}>
                    <XCircle size={14} /> Reject
                  </button>
                </div>
              </div>
            {/if}
          </div>
        {/if}
      </Card>
    {/each}
  {/if}

  <!-- Completed tasks -->
  {#if completed.length > 0}
    <div class="completed-header" onclick={() => showCompleted = !showCompleted}>
      {#if showCompleted}
        <ChevronDown size={16} />
      {:else}
        <ChevronRight size={16} />
      {/if}
      <h3>Completed ({completed.length})</h3>
    </div>

    {#if showCompleted}
      {#each completed as task (task.id)}
        <Card>
          <div class="task-header" onclick={() => toggleExpand(task.id)}>
            <div class="task-toggle">
              {#if expandedTasks[task.id]}
                <ChevronDown size={16} />
              {:else}
                <ChevronRight size={16} />
              {/if}
            </div>
            <div class="task-info">
              <span class="task-name">{task.prompt ? (task.prompt.length > 80 ? task.prompt.slice(0, 77) + '...' : task.prompt) : 'Agent task'}</span>
              <div class="task-meta">
                <span class="badge {statusBadgeClass(task.status)}">{task.status.replace(/_/g, ' ')}</span>
                {#if task.total_cost_usd > 0}
                  <span class="cost-badge">${task.total_cost_usd.toFixed(4)}</span>
                {/if}
                {#if task.team}
                  <span class="task-team"><Users size={12} /> {task.team}</span>
                {/if}
              </div>
            </div>
            <div class="task-actions">
              <button class="btn btn-sm" onclick={(e: MouseEvent) => { e.stopPropagation(); deleteTask(task.id) }} title="Delete">
                <Trash2 size={13} />
              </button>
            </div>
          </div>

          {#if expandedTasks[task.id]}
            <div class="task-detail">
              {#if task.prompt}
                <div class="task-prompt-box">{task.prompt}</div>
              {/if}

              {#if task.agent_calls && task.agent_calls.length > 0}
                <div class="agent-steps">
                  <h4>Agent Steps ({task.agent_calls.length})</h4>
                  {#each task.agent_calls as call, i}
                    {@const outputKey = `${task.id}-${i}`}
                    {@const isExpanded = expandedOutputs[outputKey]}
                    <div class="agent-step agent-step-{call.status === 'completed' ? 'completed' : call.status === 'failed' || call.status === 'timeout' ? 'failed' : 'working'}">
                      <div class="agent-step-header" onclick={() => toggleOutput(outputKey)} role="button" tabindex="0" onkeydown={(e) => e.key === 'Enter' && toggleOutput(outputKey)}>
                        {#if isExpanded}
                          <ChevronDown size={13} />
                        {:else}
                          <ChevronRight size={13} />
                        {/if}
                        <span class="agent-name">{call.agent}</span>
                        <span class="badge {statusBadgeClass(call.status)}">{call.status}</span>
                        {#if call.cost_usd > 0}
                          <span class="cost">${call.cost_usd.toFixed(4)}</span>
                        {/if}
                      </div>
                      {#if isExpanded}
                        <div class="agent-step-body">
                          {#if call.task}
                            <div class="agent-task markdown-body">{@html renderMarkdown(call.task)}</div>
                          {/if}
                          {#if call.text}
                            <div class="agent-output markdown-body">{@html renderMarkdown(call.text)}</div>
                          {/if}
                          {#if call.error}
                            <div class="agent-error">{call.error}</div>
                          {/if}
                        </div>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}

              {#if task.response}
                <div class="task-response">
                  <h4>Final Output</h4>
                  <div class="markdown-body">{@html renderMarkdown(task.response)}</div>
                </div>
              {/if}
            </div>
          {/if}
        </Card>
      {/each}
    {/if}
  {/if}

  {#if running.length === 0 && completed.length === 0}
    <div class="empty-state">
      <p>No tasks yet. Launch one above.</p>
    </div>
  {/if}
</div>

<style>
  .tasks-view {
    width: 100%;
    padding: 8px 0;
  }

  .tasks-view h2 {
    margin-bottom: 16px;
  }

  .tasks-view h3 {
    margin-bottom: 8px;
  }

  .launcher {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .form-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .form-group label {
    font-size: 0.8rem;
    font-weight: 500;
  }

  .input {
    width: 100%;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
  }

  .task-textarea {
    resize: vertical;
    min-height: 60px;
  }

  .launcher-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.82rem;
    cursor: pointer;
  }

  .task-controls {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
  }

  .task-header {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    cursor: pointer;
  }

  .task-toggle {
    padding-top: 2px;
    color: var(--text-dim);
    flex-shrink: 0;
  }

  .task-info {
    flex: 1;
    min-width: 0;
  }

  .task-name {
    font-weight: 500;
    font-size: 0.9rem;
    word-break: break-word;
  }

  .task-meta {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    margin-top: 4px;
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  .task-elapsed, .task-team, .task-iters {
    display: flex;
    align-items: center;
    gap: 3px;
  }

  .task-actions {
    flex-shrink: 0;
  }

  .badge {
    display: inline-block;
    padding: 1px 8px;
    border-radius: 12px;
    font-size: 0.7rem;
    font-weight: 500;
    text-transform: capitalize;
  }

  .badge-running {
    background: rgba(59, 130, 246, 0.15);
    color: var(--blue, #3b82f6);
  }

  .badge-completed {
    background: rgba(61, 139, 61, 0.15);
    color: var(--green, #3d8b3d);
  }

  .badge-failed, .badge-interrupted {
    background: rgba(196, 57, 42, 0.15);
    color: var(--red, #c4392a);
  }

  .badge-waiting {
    background: rgba(234, 179, 8, 0.15);
    color: var(--yellow, #eab308);
  }

  .cost {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.72rem;
    color: var(--text-dim);
  }

  .task-detail {
    margin-top: 12px;
    padding-top: 12px;
    border-top: 1px solid var(--border);
  }

  .task-prompt-box {
    padding: 10px 14px;
    margin-bottom: 12px;
    background: rgba(var(--accent-rgb, 99, 102, 241), 0.08);
    border-left: 3px solid var(--accent);
    border-radius: var(--radius, 8px);
    font-size: 0.85rem;
    line-height: 1.5;
    color: var(--text);
  }

  .cost-badge {
    display: inline-block;
    padding: 1px 8px;
    border-radius: 12px;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.7rem;
    font-weight: 500;
    background: var(--bg-input);
    color: var(--text-dim);
    border: 1px solid var(--border);
  }

  .task-detail h4 {
    font-size: 0.82rem;
    margin-bottom: 6px;
  }

  .task-plan {
    margin-bottom: 12px;
  }

  .plan-step {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 0;
    font-size: 0.82rem;
  }

  .plan-num {
    color: var(--text-dim);
    font-weight: 500;
    width: 20px;
  }

  .plan-name {
    flex: 1;
  }

  .plan-agents {
    font-size: 0.72rem;
    color: var(--text-dim);
    padding: 1px 6px;
    background: var(--bg-input);
    border-radius: 8px;
  }

  .agent-steps {
    margin-bottom: 12px;
  }

  .agent-step {
    margin-bottom: 10px;
    padding: 8px 12px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
    border-left: 3px solid var(--border);
  }

  .agent-step-completed {
    border-left-color: var(--green, #3d8b3d);
  }

  .agent-step-failed {
    border-left-color: var(--red, #c4392a);
  }

  .agent-step-working {
    border-left-color: var(--accent);
  }

  .agent-step-header {
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
    user-select: none;
    padding: 2px 0;
  }

  .agent-step-header:hover {
    opacity: 0.8;
  }

  .agent-step-body {
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid color-mix(in srgb, var(--border) 50%, transparent);
  }

  .agent-task {
    font-size: 0.8rem;
    color: var(--text-dim);
    padding: 8px 12px;
    margin-bottom: 6px;
    background: rgba(0, 0, 0, 0.04);
    border-left: 2px solid var(--border);
    border-radius: 0 var(--radius, 8px) var(--radius, 8px) 0;
    line-height: 1.5;
    word-break: break-word;
  }

  .agent-name {
    font-weight: 500;
    font-size: 0.82rem;
  }

  .agent-output {
    font-size: 0.82rem;
    line-height: 1.5;
    overflow-x: auto;
    word-break: break-word;
  }

  .agent-output-collapsed {
    max-height: 200px;
    overflow: hidden;
    position: relative;
    mask-image: linear-gradient(to bottom, black 150px, transparent 200px);
    -webkit-mask-image: linear-gradient(to bottom, black 150px, transparent 200px);
  }

  .btn-toggle-output {
    margin-top: 4px;
    border: none;
    background: none;
    color: var(--accent);
    padding: 2px 0;
    font-size: 0.75rem;
  }

  .btn-toggle-output:hover {
    background: none;
    text-decoration: underline;
  }

  .agent-error {
    color: var(--red, #c4392a);
    font-size: 0.82rem;
  }

  .task-response {
    margin-top: 12px;
  }

  .approval-ui {
    margin-top: 12px;
    padding: 12px;
    background: rgba(234, 179, 8, 0.08);
    border: 1px solid rgba(234, 179, 8, 0.2);
    border-radius: var(--radius, 8px);
  }

  .approval-ui textarea {
    margin: 8px 0;
  }

  .approval-actions {
    display: flex;
    gap: 8px;
  }

  .task-questions {
    margin-bottom: 12px;
  }

  .task-questions p {
    font-size: 0.85rem;
    padding: 4px 0;
  }

  .completed-header {
    display: flex;
    align-items: center;
    gap: 6px;
    cursor: pointer;
    margin: 16px 0 8px;
    color: var(--text-dim);
  }

  .completed-header h3 {
    margin: 0;
  }

  .empty-state {
    text-align: center;
    padding: 40px 0;
    color: var(--text-dim);
  }

  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s;
  }

  .btn:hover { background: var(--border); }
  .btn:disabled { opacity: 0.5; cursor: not-allowed; }

  .btn-primary {
    background: var(--accent);
    color: var(--on-accent);
    border-color: var(--accent);
  }

  .btn-primary:hover { opacity: 0.9; }

  .btn-sm {
    padding: 4px 10px;
    font-size: 0.75rem;
  }

  .btn-danger {
    border-color: var(--red, #c4392a);
    color: var(--red, #c4392a);
  }

  .btn-danger:hover {
    background: rgba(196, 57, 42, 0.1);
  }

  :global(.markdown-body pre) {
    background: var(--bg-input);
    padding: 8px 12px;
    border-radius: var(--radius, 8px);
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.8rem;
  }

  :global(.markdown-body code) {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.82rem;
  }

  :global(.markdown-body code:not(pre code)) {
    background: var(--bg-input);
    padding: 1px 4px;
    border-radius: 3px;
  }

  :global(.markdown-body h1, .markdown-body h2, .markdown-body h3, .markdown-body h4) {
    margin: 12px 0 6px;
    font-weight: 600;
  }

  :global(.markdown-body p) {
    margin-bottom: 8px;
  }

  :global(.markdown-body ul, .markdown-body ol) {
    padding-left: 20px;
    margin-bottom: 8px;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }

  :global(.spin) {
    animation: spin 1s linear infinite;
  }
</style>
