<script>
  import { onMount } from 'svelte';
  import { api, esc } from '../lib/api';
  import { toasts } from '../stores/toast.svelte';
  import { events } from '../stores/events.svelte';
  import Card from '../components/shared/Card.svelte';
  import Modal from '../components/shared/Modal.svelte';
  import Toggle from '../components/shared/Toggle.svelte';
  import { Plus, Pencil, Trash2, Save, Copy, Router, Brain, ChevronDown } from 'lucide-svelte';

  // --- State ---
  let tiersConfig = $state(null);
  let availableBackends = $state([]);
  let availableTools = $state([]);
  let backendModels = $state({});
  let providerSchemas = $state([]);
  let availableClaudeModels = $state([]); // user-editable list from /api/models/claude
  let loading = $state(true);

  // Config profiles
  let configs = $state([]);
  let activeConfigName = $state('');

  // Modals
  let showTierModal = $state(false);
  let showRouterModal = $state(false);
  let showMemoryModal = $state(false);
  let editingTierIndex = $state(-1);

  // Tier form
  let tierForm = $state({
    name: '', backend: '', model: '', routable: true, enabled: true,
    router_label: '', description: '', effort: '', max_turns: 0,
    write_capable: false, tools: [], system_prompt: '', context_weight: 'full',
    priority: 0, force_command: false, max_iterations: 0, timeout_minutes: 0,
    orchestrator_max_turns: 0, fallback: '', role: '',
  });

  // Router form
  let routerForm = $state({ router_model: '', router_backend: '', default_fallback: '', router_distinctions: '' });

  // Memory form
  let memoryForm = $state({ extract_backend: '', extract_model: '' });

  // --- Derived ---
  let modelsForBackend = $derived(
    tierForm.backend && backendModels[tierForm.backend]
      ? backendModels[tierForm.backend]
      : []
  );

  let routerModelsForBackend = $derived(
    routerForm.router_backend && backendModels[routerForm.router_backend]
      ? backendModels[routerForm.router_backend]
      : []
  );

  let memoryModelsForBackend = $derived(
    memoryForm.extract_backend && backendModels[memoryForm.extract_backend]
      ? backendModels[memoryForm.extract_backend]
      : []
  );

  // Tool mode: 'all' = ["*"], 'native' = ["*native"], 'custom' = individual selection.
  let toolMode = $derived(
    tierForm.tools.length === 1 && tierForm.tools[0] === '*' ? 'all'
    : tierForm.tools.length === 1 && tierForm.tools[0] === '*native' ? 'native'
    : 'custom'
  );

  // Selected model's info (for capability warnings).
  let selectedModelInfo = $derived(() => {
    const models = tierForm.backend && backendModels[tierForm.backend];
    if (!models) return null;
    return models.find(m => m.id === tierForm.model) || null;
  });

  // Current provider schema for selected backend.
  let selectedProvider = $derived(
    providerSchemas.find(p => p.id === (tierForm.backend || 'cli')) || null
  );

  // Configured vs unconfigured providers.
  let configuredProviders = $derived(providerSchemas.filter(p => p.configured));
  let unconfiguredProviders = $derived(providerSchemas.filter(p => !p.configured));

  let hasOrchestratorTier = $derived(
    tiersConfig?.tiers?.some(t => t.role === 'orchestrator' && t.enabled) ?? false
  );

  // --- Load ---
  async function loadTiers() {
    loading = true;
    try {
      const data = await api('GET', '/api/tiers');
      tiersConfig = { tiers: data.tiers || [], router_model: data.router_model || '', default_fallback: data.default_fallback || '', router_distinctions: data.router_distinctions || '', router_backend: data.router_backend || '', memory: data.memory || {} };
      availableBackends = data.available_backends || [];
      availableTools = data.available_tools || [];
      backendModels = data.backend_models || {};
      providerSchemas = data.provider_schemas || [];
    } catch (e) {
      toasts.error('Failed to load tiers: ' + e.message);
    } finally {
      loading = false;
    }
  }

  async function loadClaudeModels() {
    try {
      const data = await api('GET', '/api/models/claude');
      availableClaudeModels = Array.isArray(data?.models) ? data.models : [];
    } catch {
      availableClaudeModels = [];
    }
  }

  async function loadConfigs() {
    try {
      const data = await api('GET', '/api/tiers/configs');
      configs = Array.isArray(data) ? data : [];
      const active = configs.find(c => c.active);
      if (active) {
        activeConfigName = active.name;
      }
    } catch {}
  }

  let switching = false
  async function switchConfig(name) {
    if (!name || switching) return;
    switching = true;
    try {
      await api('POST', '/api/tiers/configs/switch', { name: name + '.json' });
      toasts.success(`Switched to profile: ${name}`);
      await loadTiers();
      await loadConfigs();
    } catch (e) {
      toasts.error('Switch failed: ' + e.message);
      await loadConfigs(); // restore select to actual active
    } finally {
      switching = false;
    }
  }

  async function duplicateConfig() {
    const source = configs.find(c => c.active);
    if (!source) return;
    const newName = prompt('New profile name:', source.name + '-copy');
    if (!newName) return;
    try {
      await api('POST', '/api/tiers/configs/duplicate', { source: source.name + '.json', name: newName + '.json' });
      toasts.success(`Duplicated to: ${newName}`);
      await loadConfigs();
    } catch (e) {
      toasts.error('Duplicate failed: ' + e.message);
    }
  }

  // --- Tier CRUD ---
  function applyProviderHints(backendId) {
    const schema = providerSchemas.find(p => p.id === (backendId || 'cli'));
    if (!schema?.default_hints) return;
    const h = schema.default_hints;
    if (h.effort) tierForm.effort = h.effort;
    if (h.context_weight) tierForm.context_weight = h.context_weight;
    if (h.write_capable !== undefined && h.write_capable !== null) tierForm.write_capable = h.write_capable;
    if (h.tools?.length) tierForm.tools = [...h.tools];
    if (h.max_turns) tierForm.max_turns = h.max_turns;
    if (h.timeout_min) tierForm.timeout_minutes = h.timeout_min;
  }

  function onBackendChange(newBackend) {
    tierForm.backend = newBackend;
    if (editingTierIndex < 0) {
      applyProviderHints(newBackend);
    }
  }

  function openAddTier() {
    editingTierIndex = -1;
    tierForm = { name: '', backend: 'cli', model: 'sonnet', routable: true, enabled: true, router_label: '', description: '', effort: '', max_turns: 0, write_capable: false, tools: [], system_prompt: '', context_weight: 'full', priority: 0, force_command: false, max_iterations: 0, timeout_minutes: 0, orchestrator_max_turns: 0, fallback: '', role: '' };
    applyProviderHints('cli');
    showTierModal = true;
  }

  function openEditTier(idx) {
    editingTierIndex = idx;
    const t = tiersConfig.tiers[idx];
    tierForm = { ...t, tools: [...(t.tools || [])] };
    showTierModal = true;
  }

  async function saveTierForm() {
    if (!tierForm.name.trim()) { toasts.error('Name is required'); return; }
    const tier = { ...tierForm };
    if (editingTierIndex >= 0) {
      tiersConfig.tiers[editingTierIndex] = tier;
    } else {
      tiersConfig.tiers.push(tier);
    }
    showTierModal = false;
    await saveAll();
  }

  function duplicateTier(idx) {
    const src = tiersConfig.tiers[idx];
    const copy = { ...src, tools: [...(src.tools || [])], name: src.name + '-copy' };
    editingTierIndex = -1;
    tierForm = copy;
    showTierModal = true;
  }

  async function deleteTier(idx) {
    if (!confirm(`Delete tier "${tiersConfig.tiers[idx].name}"?`)) return;
    tiersConfig.tiers.splice(idx, 1);
    tiersConfig = tiersConfig;
    await saveAll();
  }

  // --- Router modal ---
  function openRouterModal() {
    routerForm = { router_model: tiersConfig.router_model || '', router_backend: tiersConfig.router_backend || '', default_fallback: tiersConfig.default_fallback || '', router_distinctions: tiersConfig.router_distinctions || '' };
    showRouterModal = true;
  }

  async function saveRouterForm() {
    tiersConfig.router_model = routerForm.router_model;
    tiersConfig.router_backend = routerForm.router_backend;
    tiersConfig.default_fallback = routerForm.default_fallback;
    tiersConfig.router_distinctions = routerForm.router_distinctions;
    showRouterModal = false;
    await saveAll();
  }

  // --- Memory modal ---
  function openMemoryModal() {
    const m = tiersConfig.memory || {};
    memoryForm = { extract_backend: m.extract_backend || '', extract_model: m.extract_model || '' };
    showMemoryModal = true;
  }

  async function saveMemoryForm() {
    tiersConfig.memory = { ...tiersConfig.memory, extract_backend: memoryForm.extract_backend, extract_model: memoryForm.extract_model };
    showMemoryModal = false;
    await saveAll();
  }

  // --- Save all ---
  async function saveAll() {
    try {
      const payload = {
        tiers: tiersConfig.tiers,
        router_model: tiersConfig.router_model,
        default_fallback: tiersConfig.default_fallback,
        router_distinctions: tiersConfig.router_distinctions,
        router_backend: tiersConfig.router_backend,
        memory: tiersConfig.memory,
      };
      await api('PUT', '/api/tiers', payload);
      toasts.success('Tiers saved — hot-reload triggered');
    } catch (e) {
      toasts.error('Save failed: ' + e.message);
    }
  }

  function setToolMode(mode) {
    if (mode === 'all') {
      tierForm.tools = ['*'];
    } else if (mode === 'native') {
      tierForm.tools = ['*native'];
    } else {
      // Expand wildcard to individual tool names.
      if (tierForm.tools.length === 1 && (tierForm.tools[0] === '*' || tierForm.tools[0] === '*native')) {
        tierForm.tools = availableTools.map(t => t.name);
      }
    }
  }

  function toggleTool(toolName) {
    // If in wildcard mode, expand first.
    if (toolMode !== 'custom') {
      tierForm.tools = availableTools.map(t => t.name);
    }
    const idx = tierForm.tools.indexOf(toolName);
    if (idx >= 0) {
      tierForm.tools.splice(idx, 1);
    } else {
      tierForm.tools.push(toolName);
    }
    tierForm.tools = [...tierForm.tools];
  }

  function tierTypeBadge(tier) {
    if (!tier.routable) return 'system';
    if (tier.force_command) return 'command';
    return 'general';
  }

  onMount(() => {
    loadTiers();
    loadConfigs();
    loadClaudeModels();
    const unsubTiers = events.subscribe('tiers', () => { loadTiers(); loadConfigs(); });
    const unsubModels = events.subscribe('claude_models', () => { loadClaudeModels(); });
    return () => { unsubTiers(); unsubModels(); };
  });
</script>

<div class="view-tiers">
  <h2>Tiers</h2>
  <div class="toolbar">
    {#if configs.length > 0}
      <select class="select" value={activeConfigName} onchange={(e) => { const v = e.target.value; activeConfigName = v; switchConfig(v); }}>
        {#each configs as cfg}
          <option value={cfg.name}>{cfg.name} ({cfg.tiers} tiers){cfg.active ? ' ●' : ''}</option>
        {/each}
      </select>
      <button class="btn btn-ghost btn-sm" onclick={duplicateConfig} title="Duplicate profile">
        <Copy size={14} />
      </button>
    {/if}
    <button class="btn btn-ghost btn-sm" onclick={openRouterModal} title="Router config">
      <Router size={14} /> Router
    </button>
    <button class="btn btn-ghost btn-sm" onclick={openMemoryModal} title="Memory extraction LLM">
      <Brain size={14} /> Memory LLM
    </button>
    <button class="btn btn-primary btn-sm" onclick={openAddTier}>
      <Plus size={14} /> Add Tier
    </button>
  </div>

  {#if !loading && tiersConfig && !hasOrchestratorTier}
    <div class="info-banner">
      <strong>No orchestrator tier.</strong> Agent teams require a tier with role <em>orchestrator</em> to function. Add one or assign the role to an existing tier.
    </div>
  {/if}

  {#if loading}
    <div class="loading">Loading tiers...</div>
  {:else if !tiersConfig}
    <div class="empty">No tier configuration loaded.</div>
  {:else}
    <div class="tier-grid">
      {#each tiersConfig.tiers as tier, idx (tier.name + idx)}
        <Card>
          <div class="tier-card">
            <div class="tier-header">
              <strong>{tier.name}</strong>
              <span class="badge badge-type">{tierTypeBadge(tier)}</span>
              {#if !tier.enabled}
                <span class="badge badge-disabled">disabled</span>
              {/if}
            </div>
            <div class="tier-meta">
              <span>Backend: <strong>{tier.backend || 'cli'}</strong></span>
              <span>Model: <strong>{tier.model}</strong></span>
              {#if tier.effort}
                <span>Effort: {tier.effort}</span>
              {/if}
              {#if tier.max_turns}
                <span>Max turns: {tier.max_turns}</span>
              {/if}
            </div>
            {#if tier.role === 'orchestrator'}
              <span class="tier-fallback">Role: <strong>orchestrator</strong></span>
            {/if}
            {#if tier.fallback}
              <span class="tier-fallback">Fallback → <strong>{tier.fallback}</strong></span>
            {/if}
            {#if tier.description || tier.router_label}
              <p class="tier-desc">{tier.description || tier.router_label}</p>
            {/if}
            <div class="tier-actions">
              <button class="btn btn-ghost btn-sm" onclick={() => openEditTier(idx)}>
                <Pencil size={12} /> Edit
              </button>
              <button class="btn btn-ghost btn-sm" onclick={() => duplicateTier(idx)}>
                <Copy size={12} /> Duplicate
              </button>
              <button class="btn btn-ghost btn-sm btn-danger" onclick={() => deleteTier(idx)}>
                <Trash2 size={12} /> Delete
              </button>
            </div>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<!-- Tier Add/Edit Modal -->
<Modal open={showTierModal} wide onclose={() => showTierModal = false}>
  <h3>{editingTierIndex >= 0 ? 'Edit' : 'Add'} Tier</h3>
      <div class="form-grid">
        <label>
          Name
          <input type="text" bind:value={tierForm.name} placeholder="e.g. fast-tier" />
        </label>
        <label>
          Backend
          <select value={tierForm.backend} onchange={(e) => onBackendChange(e.target.value)}>
            {#if configuredProviders.length > 0}
              <optgroup label="Configured">
                {#each configuredProviders as p}
                  <option value={p.id === 'cli' ? '' : p.id}>{p.name}</option>
                {/each}
              </optgroup>
            {/if}
            {#if unconfiguredProviders.length > 0}
              <optgroup label="Available (setup required)">
                {#each unconfiguredProviders as p}
                  <option value={p.id}>{p.name} (setup required)</option>
                {/each}
              </optgroup>
            {/if}
          </select>
          {#if selectedProvider && !selectedProvider.configured}
            <span class="form-warning">This backend needs API key configuration in Settings.</span>
          {/if}
        </label>
        <label>
          Model
          {#if modelsForBackend.length > 0}
            <select bind:value={tierForm.model}>
              <option value="">-- select --</option>
              {#each modelsForBackend as m}
                <option value={m.id}>{m.id}{m.tool_calls === false ? ' (no tools)' : ''}</option>
              {/each}
            </select>
          {:else}
            <select bind:value={tierForm.model}>
              <option value="">-- select --</option>
              {#each availableClaudeModels as m}
                <option value={m}>{m}</option>
              {/each}
            </select>
          {/if}
          {#if selectedModelInfo()?.tool_calls === false && tierForm.tools.length > 0}
            <span class="form-warning">This model does not support tool calling.</span>
          {/if}
        </label>
        {#if selectedProvider?.supports_effort !== false}
        <label>
          Effort
          <select bind:value={tierForm.effort}>
            <option value="">default</option>
            <option value="low">low</option>
            <option value="medium">medium</option>
            <option value="high">high</option>
            <option value="max">max</option>
          </select>
        </label>
        {/if}
        <label>
          Max Turns
          <input type="number" bind:value={tierForm.max_turns} min="0" />
        </label>
        <label>
          Priority
          <input type="number" bind:value={tierForm.priority} />
        </label>
        <label>
          Context Weight
          <select bind:value={tierForm.context_weight}>
            <option value="full">full</option>
            <option value="standard">standard</option>
            <option value="light">light</option>
          </select>
        </label>
        <label>
          Timeout (min)
          <input type="number" bind:value={tierForm.timeout_minutes} min="0" />
        </label>
        <label>
          Role
          <select bind:value={tierForm.role}>
            <option value="">default</option>
            <option value="orchestrator">orchestrator</option>
          </select>
        </label>
        <label>
          Fallback Tier
          <select bind:value={tierForm.fallback}>
            <option value="">none</option>
            {#if tiersConfig}
              {#each tiersConfig.tiers as t}
                {#if t.name !== tierForm.name}
                  <option value={t.name}>{t.name}</option>
                {/if}
              {/each}
            {/if}
          </select>
        </label>
        <label class="full-width">
          Router Label
          <textarea bind:value={tierForm.router_label} rows="3" placeholder="Describe when the router should pick this tier"></textarea>
        </label>
        <label class="full-width">
          Description
          <input type="text" bind:value={tierForm.description} placeholder="Description for the router" />
        </label>
        <label class="full-width">
          System Prompt
          <textarea bind:value={tierForm.system_prompt} rows="3" placeholder="Extra system prompt for this tier"></textarea>
        </label>
        <div class="checkbox-row">
          <Toggle bind:checked={tierForm.enabled} label="Enabled" />
          <Toggle bind:checked={tierForm.routable} label="Routable" />
          {#if selectedProvider?.supports_writing !== false}
            <Toggle bind:checked={tierForm.write_capable} label="Write Capable" />
          {/if}
          <Toggle bind:checked={tierForm.force_command} label="Force Command" />
        </div>
        {#if availableTools.length > 0}
          <div class="full-width">
            <strong>Tools</strong>
            <div class="tool-mode-row">
              <div class="btn-group">
                <button class="btn btn-sm" class:active={toolMode === 'all'} onclick={() => setToolMode('all')}>All Tools</button>
                {#if selectedProvider?.has_native_tools}
                  <button class="btn btn-sm" class:active={toolMode === 'native'} onclick={() => setToolMode('native')}>Native Only</button>
                {/if}
                <button class="btn btn-sm" class:active={toolMode === 'custom'} onclick={() => setToolMode('custom')}>Select</button>
              </div>
              {#if toolMode === 'all'}
                <span class="form-hint">All {availableTools.length} tools enabled (wildcard).</span>
              {:else if toolMode === 'native'}
                <span class="form-hint">Only native Go tools (bash, read, write, grep, glob).</span>
              {:else}
                <span class="form-hint">{tierForm.tools.length} of {availableTools.length} tools selected.</span>
              {/if}
            </div>
            {#if toolMode === 'custom'}
              <div class="tool-list">
                {#each availableTools as tool}
                  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
                  <div class="list-item-interactive" class:selected={tierForm.tools.includes(tool.name)} onclick={() => toggleTool(tool.name)}>
                    <input type="checkbox" checked={tierForm.tools.includes(tool.name)} onclick={(e) => e.stopPropagation()} onchange={() => toggleTool(tool.name)} />
                    <span>{tool.name}</span>
                    <span class="text-dim" style="margin-left:auto; font-size: var(--font-xs, 11px)">{tool.source}</span>
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
      </div>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showTierModal = false}>Cancel</button>
    <button class="btn btn-primary" onclick={saveTierForm}>
      {editingTierIndex >= 0 ? 'Update' : 'Add'}
    </button>
  </div>
</Modal>

<!-- Router Config Modal -->
<Modal open={showRouterModal} onclose={() => showRouterModal = false}>
  <h3><Router size={18} /> Router Configuration</h3>
      <div class="form-grid">
        <label>
          Router Backend
          <select bind:value={routerForm.router_backend}>
            <option value="">cli (default)</option>
            {#each providerSchemas as p}
              {#if p.id !== 'cli'}
                <option value={p.id}>{p.name}{!p.configured ? ' (setup required)' : ''}</option>
              {/if}
            {/each}
          </select>
        </label>
        <label>
          Router Model
          {#if routerModelsForBackend.length > 0}
            <select bind:value={routerForm.router_model}>
              <option value="">-- select --</option>
              {#each routerModelsForBackend as m}
                <option value={m.id}>{m.id}</option>
              {/each}
            </select>
          {:else}
            <select bind:value={routerForm.router_model}>
              <option value="">-- select --</option>
              {#each availableClaudeModels as m}
                <option value={m}>{m}</option>
              {/each}
            </select>
          {/if}
        </label>
        <label class="full-width">
          Default Fallback Tier
          <select bind:value={routerForm.default_fallback}>
            <option value="">none</option>
            {#if tiersConfig}
              {#each tiersConfig.tiers as t}
                <option value={t.name}>{t.name}</option>
              {/each}
            {/if}
          </select>
        </label>
        <label class="full-width">
          Router Distinctions
          <textarea bind:value={routerForm.router_distinctions} rows="3" placeholder="Extra instructions for the classifier"></textarea>
        </label>
      </div>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showRouterModal = false}>Cancel</button>
    <button class="btn btn-primary" onclick={saveRouterForm}>Save</button>
  </div>
</Modal>

<!-- Memory Config Modal -->
<Modal open={showMemoryModal} onclose={() => showMemoryModal = false}>
  <h3><Brain size={18} /> Memory Extraction LLM</h3>
      <p class="modal-hint">Which LLM analyzes conversations to extract facts and learnings into long-term memory. By default, uses the same backend and model as the router (cheap and fast).</p>
      <div class="form-grid">
        <label>
          Backend
          <select bind:value={memoryForm.extract_backend}>
            <option value="">same as router</option>
            {#each providerSchemas as p}
              <option value={p.id}>{p.name}{!p.configured ? ' (setup required)' : ''}</option>
            {/each}
          </select>
        </label>
        <label>
          Model
          {#if memoryModelsForBackend.length > 0}
            <select bind:value={memoryForm.extract_model}>
              <option value="">same as router</option>
              {#each memoryModelsForBackend as m}
                <option value={m.id}>{m.id}</option>
              {/each}
            </select>
          {:else}
            <input type="text" bind:value={memoryForm.extract_model} placeholder="leave empty to use router model" />
          {/if}
        </label>
      </div>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showMemoryModal = false}>Cancel</button>
    <button class="btn btn-primary" onclick={saveMemoryForm}>Save</button>
  </div>
</Modal>

<style>
  .view-tiers {
    padding: 8px 0;
    width: 100%;
  }
  .view-tiers h2 {
    margin-bottom: 16px;
  }
  .loading, .empty {
    text-align: center;
    padding: 3rem;
    color: var(--text-dim);
  }
  .tier-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
    gap: 1rem;
  }
  .tier-card {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .tier-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .badge-type {
    background: var(--sapphire);
    color: #fff;
  }
  .badge-disabled {
    background: var(--yellow);
    color: #fff;
  }
  .tier-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.8rem;
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
  }
  .tier-fallback {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
  }
  .tier-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin: 0;
  }
  .tier-actions {
    display: flex;
    gap: 0.3rem;
    margin-top: 0.2rem;
  }
  .select {
    padding: 0.3rem 0.5rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--bg-card);
    color: var(--text);
    font-size: var(--font-sm, 13px);
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
  .checkbox-row {
    grid-column: 1 / -1;
    display: flex;
    flex-wrap: wrap;
    gap: 1rem;
  }
  .checkbox {
    display: flex;
    align-items: center;
    gap: 0.3rem;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
  }
  .tool-checkboxes {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 0.3rem;
    margin-top: 0.3rem;
    max-height: 200px;
    overflow-y: auto;
  }
  .tool-check {
    font-size: var(--font-xs, 11px);
  }
  .tool-source {
    color: var(--text-dim);
    font-size: var(--font-xs, 11px);
  }
  .tool-mode-row {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    margin: 0.3rem 0;
  }
  .form-warning {
    font-size: var(--font-xs, 11px);
    color: var(--yellow, #e5a50a);
    margin-top: 2px;
  }
  .form-hint {
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }
  .modal-hint {
    font-size: var(--font-sm, 13px);
    color: var(--text-muted, #888);
    margin: 0 0 1rem 0;
    line-height: 1.4;
  }
  .modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 0.5rem;
    margin-top: 1rem;
  }
  .info-banner {
    padding: 0.6rem 1rem;
    margin-bottom: 1rem;
    border-radius: 6px;
    background: rgba(var(--yellow-rgb, 250,200,50), 0.12);
    border: 1px solid rgba(var(--yellow-rgb, 250,200,50), 0.3);
    font-size: var(--font-sm, 13px);
    color: var(--text);
  }
  .info-banner em {
    background: var(--bg-input);
    padding: 0.1rem 0.3rem;
    border-radius: 3px;
    font-style: normal;
  }
</style>
