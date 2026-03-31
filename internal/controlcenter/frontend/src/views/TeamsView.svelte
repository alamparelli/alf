<script lang="ts">
  import { onMount } from 'svelte'
  import { Plus, Pencil, Trash2, Save, X } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'

  interface Agent {
    name: string
    description: string
    tier: string
    system_prompt: string
  }

  interface Team {
    id: string
    name: string
    description: string
    orchestrator_prompt: string
    max_agents_per_request: number
    global_timeout_minutes: number
    agents: Agent[]
  }

  let teams = $state<Team[]>([])
  let editing = $state(false)
  let editorJson = $state('')
  let editorError = $state('')
  let editingTeamId = $state('')
  let loading = $state(false)

  const TEMPLATE: Team = {
    id: '',
    name: 'my-team',
    description: 'A new agent team',
    orchestrator_prompt: '',
    max_agents_per_request: 3,
    global_timeout_minutes: 10,
    agents: [
      {
        name: 'researcher',
        description: 'Finds information and does research',
        tier: 'default',
        system_prompt: 'You are a research agent.'
      }
    ]
  }

  async function loadTeams() {
    loading = true
    try {
      const data = await api<{ teams: Team[] }>('/api/teams')
      teams = data.teams || []
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load teams', 'error')
    } finally {
      loading = false
    }
  }

  function startCreate() {
    editingTeamId = ''
    editorJson = JSON.stringify(TEMPLATE, null, 2)
    editorError = ''
    editing = true
  }

  function startEdit(team: Team) {
    editingTeamId = team.id
    editorJson = JSON.stringify(team, null, 2)
    editorError = ''
    editing = true
  }

  function cancelEdit() {
    editing = false
    editorJson = ''
    editorError = ''
    editingTeamId = ''
  }

  async function saveTeam() {
    editorError = ''
    let parsed: Team
    try {
      parsed = JSON.parse(editorJson)
    } catch {
      editorError = 'Invalid JSON'
      return
    }

    if (!parsed.name) {
      editorError = 'Team name is required'
      return
    }

    // Preserve ID for existing teams
    if (editingTeamId) {
      parsed.id = editingTeamId
    }

    try {
      await api('/api/teams', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(parsed)
      })
      toasts.show('Team saved', 'success')
      cancelEdit()
      await loadTeams()
    } catch (e: any) {
      editorError = e.error || 'Save failed'
    }
  }

  async function deleteTeam(team: Team) {
    if (!confirm(`Delete team "${team.name}"?`)) return
    try {
      const params = team.id
        ? `id=${encodeURIComponent(team.id)}`
        : `name=${encodeURIComponent(team.name)}`
      await api(`/api/teams?${params}`, { method: 'DELETE' })
      toasts.show('Team deleted', 'success')
      await loadTeams()
    } catch (e: any) {
      toasts.show(e.error || 'Delete failed', 'error')
    }
  }

  function tierColor(tier: string): string {
    switch (tier) {
      case 'fast': return 'var(--green, #6a6)'
      case 'default': return 'var(--accent)'
      case 'heavy': return 'var(--yellow, #eb6)'
      case 'code': return 'var(--blue, #68d)'
      default: return 'var(--text-dim)'
    }
  }

  onMount(() => {
    loadTeams()
  })
</script>

<div class="teams-view">
  <div class="header">
    <h2>Agent Teams</h2>
    {#if !editing}
      <button class="btn-primary" onclick={startCreate}>
        <Plus size={16} /> New Team
      </button>
    {/if}
  </div>

  {#if editing}
    <Card>
      <div class="editor-header">
        <h3>{editingTeamId ? 'Edit Team' : 'New Team'}</h3>
        <button class="btn-icon" onclick={cancelEdit} title="Cancel">
          <X size={16} />
        </button>
      </div>

      <textarea
        class="json-editor"
        bind:value={editorJson}
        spellcheck="false"
        rows="20"
      ></textarea>

      {#if editorError}
        <p class="editor-error">{editorError}</p>
      {/if}

      <div class="editor-actions">
        <button class="btn-primary" onclick={saveTeam}>
          <Save size={16} /> Save
        </button>
        <button class="btn-secondary" onclick={cancelEdit}>Cancel</button>
      </div>
    </Card>
  {/if}

  {#if !editing}
    {#each teams as team}
      <Card>
        <div class="team-card">
          <div class="team-info">
            <h3>{team.name}</h3>
            <p class="team-desc">{team.description}</p>
            <div class="agent-badges">
              {#each team.agents || [] as agent}
                <span class="agent-badge" style="border-color: {tierColor(agent.tier)}">
                  {agent.name}
                  <span class="tier-label" style="color: {tierColor(agent.tier)}">{agent.tier}</span>
                </span>
              {/each}
            </div>
          </div>
          <div class="team-actions">
            <button class="btn-icon" onclick={() => startEdit(team)} title="Edit">
              <Pencil size={16} />
            </button>
            <button class="btn-icon btn-danger" onclick={() => deleteTeam(team)} title="Delete">
              <Trash2 size={16} />
            </button>
          </div>
        </div>
      </Card>
    {:else}
      <p class="empty">{loading ? 'Loading...' : 'No teams configured'}</p>
    {/each}
  {/if}
</div>

<style>
  .teams-view {
    padding: 8px 0;
    width: 100%;
  }
  .teams-view h2 {
    margin-bottom: 16px;
  }

  .header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1rem;
  }

  .btn-primary {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    background: var(--accent);
    color: var(--bg);
    border: none;
    border-radius: 6px;
    padding: 8px 16px;
    font-size: var(--font-sm, 13px);
    font-weight: 600;
    cursor: pointer;
  }

  .btn-primary:hover {
    opacity: 0.9;
  }

  .btn-secondary {
    background: none;
    border: none;
    border-radius: 6px;
    color: var(--text-dim);
    padding: 8px 16px;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
  }

  .btn-secondary:hover {
    color: var(--text);
  }

  .btn-icon {
    background: none;
    border: none;
    border-radius: 6px;
    color: var(--text-dim);
    padding: 6px;
    cursor: pointer;
    display: flex;
    align-items: center;
  }

  .btn-icon:hover {
    color: var(--accent);
  }

  .btn-danger:hover {
    color: var(--red, #e55);
    border-color: var(--red, #e55);
  }

  .team-card {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 1rem;
  }

  .team-info {
    flex: 1;
    min-width: 0;
  }

  .team-info h3 {
    font-size: var(--font-md, 15px);
    margin-bottom: 0.25rem;
  }

  .team-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin-bottom: 0.5rem;
  }

  .team-actions {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
  }

  .agent-badges {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }

  .agent-badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    background: var(--bg);
    border-radius: 12px;
    padding: 2px 10px;
    font-size: var(--font-xs, 11px);
  }

  .tier-label {
    font-size: var(--font-xs, 11px);
    opacity: 0.8;
  }

  .editor-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 0.75rem;
  }

  .editor-header h3 {
    font-size: var(--font-md, 15px);
  }

  .json-editor {
    width: 100%;
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 12px;
    font-family: 'JetBrains Mono', 'Fira Code', monospace;
    font-size: var(--font-sm, 13px);
    line-height: 1.5;
    resize: vertical;
  }

  .editor-error {
    color: var(--red, #e55);
    font-size: var(--font-sm, 13px);
    margin-top: 0.5rem;
  }

  .editor-actions {
    display: flex;
    gap: 0.75rem;
    margin-top: 0.75rem;
  }

  .empty {
    text-align: center;
    color: var(--text-dim);
    padding: 2rem;
  }
</style>
