<script>
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toasts } from '../stores/toast.svelte';
  import Card from '../components/shared/Card.svelte';
  import Modal from '../components/shared/Modal.svelte';
  import { Plus, Pencil, Trash2, Download, Zap, Shield, AlertTriangle, Check, Loader2, Eye, Lock, Copy } from 'lucide-svelte';

  // --- State ---
  let skills = $state([]);
  let tierNames = $state([]);
  let loading = $state(true);

  // Modals
  let showCreateModal = $state(false);
  let showImportModal = $state(false);
  let showEditModal = $state(false);
  let showPreviewModal = $state(false);

  // Create form
  let createForm = $state({ name: '', description: '', triggers: '', tier: 'sonnet', content: '' });

  // Edit form
  let editForm = $state({ name: '', content: '' });

  // Import form
  let importForm = $state({ command: '', backend: '', model: '' });
  let importPhase = $state('input'); // input | scanning | preview | installing
  let scanResult = $state(null);

  // Preview
  let previewSkill = $state(null);

  // Derived: split by source
  let systemSkills = $derived(skills.filter(s => s.source === 'system'));
  let userSkills = $derived(skills.filter(s => s.source === 'user'));

  // --- Load ---
  async function loadSkills() {
    loading = true;
    try {
      const data = await api('GET', '/api/skills/catalog');
      skills = (data.skills || []).map(s => ({
        ...s,
        triggers: Array.isArray(s.triggers) ? s.triggers.join(', ') : (s.triggers || ''),
      }));
    } catch (e) {
      toasts.error('Failed to load skills: ' + e.message);
    } finally {
      loading = false;
    }
  }

  // --- Create ---
  function openCreate() {
    createForm = { name: '', description: '', triggers: '', tier: '', content: '' };
    showCreateModal = true;
  }

  async function saveCreate() {
    if (!createForm.name.trim()) { toasts.error('Name is required'); return; }
    if (!createForm.content.trim()) { toasts.error('Content is required'); return; }

    // Build SKILL.md with frontmatter
    let md = '---\n';
    md += `name: ${createForm.name.trim()}\n`;
    if (createForm.description.trim()) md += `description: ${createForm.description.trim()}\n`;
    if (createForm.triggers.trim()) md += `triggers: ${createForm.triggers.trim()}\n`;
    if (createForm.tier.trim()) md += `tier: ${createForm.tier.trim()}\n`;
    md += '---\n\n';
    md += createForm.content.trim() + '\n';

    try {
      // Install via the import endpoint which writes to data/skills/{name}/SKILL.md
      await api('POST', '/api/skills/import', {
        action: 'install',
        name: createForm.name.trim(),
        content: md,
        triggers: createForm.triggers.trim(),
        tier: createForm.tier.trim(),
        overwrite: true,
      });
      toasts.success(`Skill "${createForm.name}" created`);
      showCreateModal = false;
      await loadSkills();
    } catch (e) {
      toasts.error('Create failed: ' + e.message);
    }
  }

  // --- Edit ---
  async function openEdit(skill) {
    // Fetch raw content via workspace API
    try {
      const data = await api('GET', `/api/workspace?path=${encodeURIComponent(skill.dir + '/SKILL.md')}`);
      editForm = { name: skill.name, content: data.content || '', dir: skill.dir, source: skill.source };
      showEditModal = true;
    } catch (e) {
      toasts.error('Failed to load skill content: ' + e.message);
    }
  }

  async function saveEdit() {
    try {
      await api('PUT', `/api/workspace?path=${encodeURIComponent(editForm.dir + '/SKILL.md')}`, { content: editForm.content });
      toasts.success(`Skill "${editForm.name}" saved`);
      showEditModal = false;
      await loadSkills();
    } catch (e) {
      toasts.error('Save failed: ' + e.message);
    }
  }

  // --- Delete ---
  async function deleteSkill(skill) {
    if (skill.source === 'system') {
      toasts.error('Cannot delete system skills');
      return;
    }
    if (!confirm(`Delete skill "${skill.name}"?`)) return;
    try {
      await api('DELETE', `/api/workspace?path=${encodeURIComponent(skill.dir)}`);
      toasts.success(`Skill "${skill.name}" deleted`);
      await loadSkills();
    } catch (e) {
      toasts.error('Delete failed: ' + e.message);
    }
  }

  // --- Import ---
  function openImport() {
    importForm = { command: '', backend: '', model: '' };
    importPhase = 'input';
    scanResult = null;
    showImportModal = true;
  }

  async function startScan() {
    if (!importForm.command.trim()) { toasts.error('Repository or command is required'); return; }
    importPhase = 'scanning';
    try {
      const result = await api('POST', '/api/skills/import', {
        action: 'scan',
        command: importForm.command.trim(),
        backend: importForm.backend || '',
        model: importForm.model || '',
      });
      scanResult = result;
      importPhase = 'preview';
    } catch (e) {
      // Check for available_skills in error response
      if (e.available_skills) {
        toasts.error(`Skill not found. Available: ${e.available_skills.join(', ')}`);
      } else {
        toasts.error('Scan failed: ' + (e.message || e.error || 'Unknown error'));
      }
      importPhase = 'input';
    }
  }

  async function installScanned() {
    if (!scanResult) return;
    importPhase = 'installing';
    try {
      await api('POST', '/api/skills/import', {
        action: 'install',
        name: scanResult.name,
        content: scanResult.content,
        triggers: (scanResult.triggers || []).join(', '),
        tier: scanResult.tier || '',
        source: scanResult.source || '',
        overwrite: false,
      });
      toasts.success(`Skill "${scanResult.name}" installed`);
      showImportModal = false;
      await loadSkills();
    } catch (e) {
      toasts.error('Install failed: ' + (e.message || e.error || 'Unknown error'));
      importPhase = 'preview';
    }
  }

  // --- Preview ---
  async function openPreview(skill) {
    try {
      const data = await api('GET', `/api/workspace?path=${encodeURIComponent(skill.dir + '/SKILL.md')}`);
      previewSkill = { ...skill, rawContent: data.content || '' };
      showPreviewModal = true;
    } catch (e) {
      toasts.error('Failed to load skill content: ' + e.message);
    }
  }

  // --- Override system skill ---
  async function overrideSkill(skill) {
    try {
      const data = await api('GET', `/api/workspace?path=${encodeURIComponent(skill.dir + '/SKILL.md')}`);
      createForm = {
        name: skill.name,
        description: skill.description || '',
        triggers: skill.triggers || '',
        tier: skill.tier || 'sonnet',
        content: data.content || '',
      };
      showCreateModal = true;
    } catch (e) {
      toasts.error('Failed to load skill: ' + e.message);
    }
  }

  function verdictColor(verdict) {
    if (verdict === 'PASS') return 'var(--green)';
    if (verdict === 'WARN') return 'var(--yellow)';
    return 'var(--red)';
  }

  async function loadTiers() {
    try {
      const data = await api('GET', '/api/tiers');
      tierNames = (data.tiers || []).filter(t => t.enabled).map(t => t.name);
    } catch { /* ignore */ }
  }

  onMount(() => {
    loadSkills();
    loadTiers();
  });
</script>

<div class="view-skills">
  <div class="view-header">
    <h2><Zap size={20} /> Skills</h2>
    <div class="toolbar">
      <button class="btn btn-primary btn-sm" onclick={openCreate}>
        <Plus size={14} /> New Skill
      </button>
      <button class="btn btn-info btn-sm" onclick={openImport}>
        <Download size={14} /> Import from GitHub
      </button>
    </div>
  </div>

  {#if loading}
    <div class="loading">Loading skills...</div>
  {:else if skills.length === 0}
    <div class="empty">
      <Zap size={32} />
      <p>No skills installed yet.</p>
      <p class="hint">Create a new skill or import one from GitHub.</p>
    </div>
  {:else}
    <!-- User Skills -->
    <div class="section-header">
      <h3>User Skills</h3>
      <span class="section-count">{userSkills.length}</span>
    </div>
    {#if userSkills.length === 0}
      <p class="section-empty">No user skills yet. Create one or import from GitHub.</p>
    {:else}
      <div class="skill-grid">
        {#each userSkills as skill (skill.name)}
          <Card>
            <div class="skill-card">
              <div class="skill-header">
                <strong>{skill.name}</strong>
                {#if skill.tier}
                  <span class="badge badge-tier">{skill.tier}</span>
                {/if}
              </div>
              {#if skill.description}
                <p class="skill-desc">{skill.description}</p>
              {/if}
              {#if skill.triggers}
                <div class="skill-triggers">
                  {#each skill.triggers.split(',').map(t => t.trim()).filter(Boolean) as trigger}
                    <span class="trigger-tag">{trigger}</span>
                  {/each}
                </div>
              {/if}
              <div class="skill-meta">
                {#if skill.version}
                  <span>v{skill.version}</span>
                {/if}
                {#if skill.dir}
                  <span class="skill-dir">{skill.dir}</span>
                {/if}
              </div>
              <div class="skill-actions">
                <button class="btn btn-ghost btn-sm" onclick={() => openPreview(skill)}>
                  <Eye size={12} /> View
                </button>
                <button class="btn btn-ghost btn-sm" onclick={() => openEdit(skill)}>
                  <Pencil size={12} /> Edit
                </button>
                <button class="btn btn-ghost btn-sm btn-danger" onclick={() => deleteSkill(skill)}>
                  <Trash2 size={12} /> Delete
                </button>
              </div>
            </div>
          </Card>
        {/each}
      </div>
    {/if}

    <!-- System Skills -->
    <div class="section-header" style="margin-top: 2rem">
      <h3><Lock size={14} /> System Skills</h3>
      <span class="section-count">{systemSkills.length}</span>
    </div>
    <p class="section-hint">Built-in skills. Use Override to create a customized copy.</p>
    <div class="skill-grid">
      {#each systemSkills as skill (skill.name)}
        <Card>
          <div class="skill-card skill-card-system">
            <div class="skill-header">
              <strong>{skill.name}</strong>
              {#if skill.tier}
                <span class="badge badge-tier">{skill.tier}</span>
              {/if}
              <span class="badge badge-system">system</span>
            </div>
            {#if skill.description}
              <p class="skill-desc">{skill.description}</p>
            {/if}
            {#if skill.triggers}
              <div class="skill-triggers">
                {#each skill.triggers.split(',').map(t => t.trim()).filter(Boolean) as trigger}
                  <span class="trigger-tag">{trigger}</span>
                {/each}
              </div>
            {/if}
            <div class="skill-meta">
              {#if skill.version}
                <span>v{skill.version}</span>
              {/if}
              {#if skill.dir}
                <span class="skill-dir">{skill.dir}</span>
              {/if}
            </div>
            <div class="skill-actions">
              <button class="btn btn-ghost btn-sm" onclick={() => openPreview(skill)}>
                <Eye size={12} /> View
              </button>
              <button class="btn btn-ghost btn-sm" onclick={() => overrideSkill(skill)} title="Copy to user skills for customization">
                <Copy size={12} /> Override
              </button>
            </div>
          </div>
        </Card>
      {/each}
    </div>
  {/if}
</div>

<!-- Create Modal -->
<Modal open={showCreateModal} onclose={() => showCreateModal = false}>
  <h3><Plus size={18} /> New Skill</h3>
  <div class="form-grid">
    <label>
      Name
      <input type="text" bind:value={createForm.name} placeholder="e.g. my-skill" />
    </label>
    <label>
      Tier
      <select bind:value={createForm.tier}>
        <option value="">auto</option>
        {#each tierNames as t}
          <option value={t}>{t}</option>
        {/each}
      </select>
    </label>
    <label class="full-width">
      Description
      <input type="text" bind:value={createForm.description} placeholder="What this skill does" />
    </label>
    <label class="full-width">
      Triggers
      <input type="text" bind:value={createForm.triggers} placeholder="comma-separated trigger words" />
    </label>
    <label class="full-width">
      Content (Markdown)
      <textarea bind:value={createForm.content} rows="12" placeholder="# My Skill&#10;&#10;Instructions for the LLM..."></textarea>
    </label>
  </div>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showCreateModal = false}>Cancel</button>
    <button class="btn btn-primary" onclick={saveCreate}>Create</button>
  </div>
</Modal>

<!-- Edit Modal -->
<Modal open={showEditModal} wide onclose={() => showEditModal = false}>
  <h3><Pencil size={18} /> Edit: {editForm.name}</h3>
  <label class="full-width">
    <textarea bind:value={editForm.content} rows="20" class="code-editor"></textarea>
  </label>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showEditModal = false}>Cancel</button>
    <button class="btn btn-primary" onclick={saveEdit}>Save</button>
  </div>
</Modal>

<!-- Import Modal -->
<Modal open={showImportModal} wide onclose={() => showImportModal = false}>
  <h3><Download size={18} /> Import Skill from GitHub</h3>

  {#if importPhase === 'input' || importPhase === 'scanning'}
    <p class="import-hint">
      Browse community skills at <a href="https://skills.sh" target="_blank" rel="noopener">skills.sh</a>, then paste the repository here.
    </p>
    <div class="form-grid">
      <label class="full-width">
        Repository
        <input type="text" bind:value={importForm.command} placeholder="owner/repo or owner/repo --skill name" disabled={importPhase === 'scanning'} />
      </label>
    </div>
    <div class="import-security-note">
      <Shield size={14} />
      <div>
        <strong>Are community skills safe?</strong>
        Every skill goes through an automated LLM security audit before installation. Skills run inside the same isolated container — no access to your host machine unless explicitly allowed.
      </div>
    </div>
    <div class="modal-actions">
      <button class="btn btn-ghost" onclick={() => showImportModal = false}>Cancel</button>
      <button class="btn btn-primary" onclick={startScan} disabled={importPhase === 'scanning'}>
        {#if importPhase === 'scanning'}
          <Loader2 size={14} class="spin" /> Scanning...
        {:else}
          <Shield size={14} /> Scan & Preview
        {/if}
      </button>
    </div>

  {:else if importPhase === 'preview' && scanResult}
    <div class="scan-result">
      <div class="scan-header">
        <strong>{scanResult.name}</strong>
        <span class="badge" style:background={verdictColor(scanResult.verdict)} style:color="#fff">
          {#if scanResult.verdict === 'PASS'}<Check size={12} />{:else}<AlertTriangle size={12} />{/if}
          {scanResult.verdict}
        </span>
      </div>
      {#if scanResult.description}
        <p class="skill-desc">{scanResult.description}</p>
      {/if}
      {#if scanResult.source}
        <p class="skill-meta">Source: {scanResult.source}</p>
      {/if}
      {#if scanResult.issues?.length}
        <div class="scan-issues">
          <strong>Issues:</strong>
          <ul>
            {#each scanResult.issues as issue}
              <li>{issue}</li>
            {/each}
          </ul>
        </div>
      {/if}
      {#if scanResult.triggers?.length}
        <div class="skill-triggers" style="margin-top: 0.5rem">
          {#each scanResult.triggers as trigger}
            <span class="trigger-tag">{trigger}</span>
          {/each}
        </div>
      {/if}
      {#if scanResult.tier}
        <p class="skill-meta">Suggested tier: <strong>{scanResult.tier}</strong></p>
      {/if}
      <details class="content-preview">
        <summary>View SKILL.md content</summary>
        <pre>{scanResult.content}</pre>
      </details>
    </div>
    <div class="modal-actions">
      <button class="btn btn-ghost" onclick={() => showImportModal = false}>Cancel</button>
      {#if scanResult.verdict === 'FAIL'}
        <button class="btn btn-warning" onclick={installScanned}>Install Anyway</button>
      {:else}
        <button class="btn btn-primary" onclick={installScanned} disabled={importPhase === 'installing'}>
          {#if importPhase === 'installing'}
            <Loader2 size={14} class="spin" /> Installing...
          {:else}
            <Download size={14} /> Install
          {/if}
        </button>
      {/if}
    </div>
  {/if}
</Modal>

<!-- Preview Modal -->
{#if previewSkill}
<Modal open={showPreviewModal} wide onclose={() => showPreviewModal = false}>
  <h3><Eye size={18} /> {previewSkill.name}</h3>
  <pre class="content-pre">{previewSkill.rawContent}</pre>
  <div class="modal-actions">
    <button class="btn btn-ghost" onclick={() => showPreviewModal = false}>Close</button>
    <button class="btn btn-primary" onclick={() => { showPreviewModal = false; openEdit(previewSkill); }}>Edit</button>
  </div>
</Modal>
{/if}

<style>
  .view-skills {
    padding: var(--space-sm, 8px) 0;
    width: 100%;
  }
  .section-header {
    display: flex;
    align-items: center;
    gap: var(--space-sm, 8px);
    margin-bottom: var(--space-sm, 8px);
  }
  .section-header h3 {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin: 0;
    font-size: var(--font-md, 15px);
  }
  .section-count {
    font-size: var(--font-xs, 11px);
    padding: 0.1rem 0.4rem;
    border-radius: 8px;
    background: var(--bg-input);
    color: var(--text-dim);
  }
  .section-empty {
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
    margin: 0 0 var(--space-md, 16px);
  }
  .section-hint {
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
    margin: -0.4rem 0 0.75rem;
  }
  .loading, .empty {
    text-align: center;
    padding: 3rem;
    color: var(--text-dim);
  }
  .empty p { margin: 0.5rem 0 0; }
  .hint { font-size: var(--font-sm, 13px); }
  .skill-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
    gap: var(--space-md, 16px);
  }
  .skill-card {
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .skill-header {
    display: flex;
    align-items: center;
    gap: var(--space-sm, 8px);
  }
  .badge-tier {
    background: var(--mauve);
    color: #fff;
  }
  .badge-system {
    background: var(--overlay0, #6c7086);
    color: #fff;
  }
  .badge-user {
    background: var(--teal);
    color: #fff;
  }
  .skill-card-system {
    opacity: 0.85;
  }
  :global(.system-lock) {
    color: var(--text-dim);
    flex-shrink: 0;
  }
  .skill-dir {
    font-family: 'Geist Mono', monospace;
    font-size: var(--font-xs, 11px);
  }
  .skill-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin: 0;
    line-height: 1.4;
  }
  .skill-triggers {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem;
  }
  .trigger-tag {
    font-size: var(--font-xs, 11px);
    padding: 0.1rem 0.4rem;
    border-radius: 3px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    color: var(--text-dim);
  }
  .skill-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 0.8rem;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
  }
  .skill-actions {
    display: flex;
    gap: 0.3rem;
    margin-top: 0.2rem;
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
    gap: var(--space-sm, 8px);
    margin-top: var(--space-md, 16px);
  }
  .code-editor {
    font-family: 'Geist Mono', monospace;
    font-size: var(--font-sm, 13px);
    line-height: 1.5;
    width: 100%;
    padding: 0.6rem;
    border: 1px solid var(--border);
    border-radius: 4px;
    background: var(--bg);
    color: var(--text);
    resize: vertical;
  }
  .content-pre {
    font-family: 'Geist Mono', monospace;
    font-size: var(--font-xs, 11px);
    line-height: 1.5;
    background: var(--bg);
    padding: var(--space-md, 16px);
    border-radius: 4px;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 60vh;
    overflow-y: auto;
  }

  /* Import hints */
  .import-hint {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin-bottom: var(--space-sm, 8px);
  }

  .import-hint a {
    color: var(--accent);
    text-decoration: none;
  }

  .import-hint a:hover { text-decoration: underline; }

  .import-security-note {
    display: flex;
    gap: 10px;
    padding: 10px 14px;
    margin-top: 0.75rem;
    background: color-mix(in srgb, var(--green) 6%, transparent);
    border-radius: var(--radius, 8px);
    border-left: 3px solid var(--green, #3d8b3d);
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    line-height: 1.5;
  }

  .import-security-note strong {
    color: var(--text);
    display: block;
    margin-bottom: 2px;
  }

  /* Scan result */
  .scan-result {
    display: flex;
    flex-direction: column;
    gap: var(--space-sm, 8px);
  }
  .scan-header {
    display: flex;
    align-items: center;
    gap: var(--space-sm, 8px);
  }
  .scan-issues {
    font-size: var(--font-sm, 13px);
    color: var(--yellow);
  }
  .scan-issues ul {
    margin: 0.2rem 0 0 1.2rem;
    padding: 0;
  }
  .content-preview {
    margin-top: var(--space-sm, 8px);
  }
  .content-preview summary {
    cursor: pointer;
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
  }
  .content-preview pre {
    font-family: 'Geist Mono', monospace;
    font-size: var(--font-xs, 11px);
    background: var(--bg);
    padding: 0.6rem;
    border-radius: 4px;
    max-height: 300px;
    overflow: auto;
    white-space: pre-wrap;
    margin-top: 0.3rem;
  }

</style>
