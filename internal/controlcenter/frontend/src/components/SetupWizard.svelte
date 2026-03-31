<script lang="ts">
  import Modal from './shared/Modal.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { nav } from '../stores/nav.svelte'
  import { CheckCircle, Loader2, XCircle, ChevronRight, ChevronLeft, SkipForward } from 'lucide-svelte'

  let { open = $bindable(false) }: { open?: boolean } = $props()

  // Mode: 'wizard' (setup incomplete) or 'welcome' (setup complete, first visit)
  let mode = $state<'wizard' | 'welcome'>('wizard')
  let step = $state(0)
  const STEPS = ['Backend', 'Telegram', 'Tiers', 'Apply', 'Get Started']

  // --- Step 0: Backends ---
  interface BackendField { key: string; label: string; placeholder: string; type?: string; defaultVal?: string }
  interface BackendDef {
    id: string; name: string; desc: string; fields: BackendField[]
    defaultURL?: string; auth?: string
  }

  const BACKENDS: BackendDef[] = [
    { id: 'claude', name: 'Claude', desc: 'Anthropic via local CLI', fields: [] },
    { id: 'codex', name: 'OpenAI Codex', desc: 'OpenAI via local CLI', fields: [
      { key: 'api_key', label: 'API Key (optional)', placeholder: 'sk-... or leave empty for codex login', type: 'password' }
    ] },
    { id: 'openrouter', name: 'OpenRouter', desc: 'Multi-model gateway', fields: [
      { key: 'api_key', label: 'API Key', placeholder: 'sk-or-...', type: 'password' }
    ], defaultURL: 'https://openrouter.ai/api/v1' },
    { id: 'openai', name: 'OpenAI', desc: 'GPT models', fields: [
      { key: 'base_url', label: 'Base URL', placeholder: 'https://api.openai.com/v1', defaultVal: 'https://api.openai.com/v1' },
      { key: 'api_key', label: 'API Key', placeholder: 'sk-...', type: 'password' }
    ], defaultURL: 'https://api.openai.com/v1' },
    { id: 'ollama', name: 'Ollama', desc: 'Local models', fields: [
      { key: 'base_url', label: 'Base URL', placeholder: 'http://host.docker.internal:11434/v1', defaultVal: 'http://host.docker.internal:11434/v1' }
    ], defaultURL: 'http://host.docker.internal:11434/v1', auth: 'none' },
    { id: 'custom', name: 'Custom', desc: 'OpenAI-compatible endpoint', fields: [
      { key: 'base_url', label: 'Base URL', placeholder: 'https://...' },
      { key: 'api_key', label: 'API Key', placeholder: 'sk-...', type: 'password' },
      { key: 'default_model', label: 'Default model', placeholder: 'model-name' }
    ] }
  ]

  let selectedBackends = $state<Set<string>>(new Set())
  let backendFields = $state<Record<string, Record<string, string>>>({})
  let testResults = $state<Record<string, { ok: boolean; msg: string; loading: boolean }>>({})
  let claudeAuth = $state<boolean | null>(null)
  let claudeChecking = $state(false)
  let ollamaModels = $state<string[]>([])
  // Two-phase backend: select first, then configure
  let backendConfigPhase = $state(false)
  let applyError = $state('')

  function toggleBackend(id: string) {
    const s = new Set(selectedBackends)
    if (s.has(id)) s.delete(id); else s.add(id)
    selectedBackends = s
    if (id === 'claude' && s.has('claude')) checkClaude()
  }

  function getField(backend: string, key: string): string {
    return backendFields[backend]?.[key] || ''
  }

  function setField(backend: string, key: string, value: string) {
    backendFields = { ...backendFields, [backend]: { ...(backendFields[backend] || {}), [key]: value } }
  }

  async function checkClaude() {
    claudeChecking = true
    try {
      const d = await api<any>('/api/setup/claude/check')
      claudeAuth = !!d.authenticated
    } catch { claudeAuth = false }
    claudeChecking = false
  }

  async function testBackend(def: BackendDef) {
    const baseURL = getField(def.id, 'base_url') || def.defaultURL || ''
    const apiKey = getField(def.id, 'api_key') || ''
    testResults = { ...testResults, [def.id]: { ok: false, msg: '', loading: true } }
    try {
      const d = await api<any>('POST', '/api/setup/backend/test', { type: def.id, base_url: baseURL, api_key: apiKey })
      testResults = { ...testResults, [def.id]: { ok: d.ok, msg: d.ok ? 'Connected' : (d.error || 'Failed'), loading: false } }
      if (def.id === 'ollama' && d.ok) loadOllamaModels()
    } catch (e: any) {
      testResults = { ...testResults, [def.id]: { ok: false, msg: e.error || 'Connection failed', loading: false } }
    }
  }

  async function loadOllamaModels() {
    const baseUrl = getField('ollama', 'base_url') || 'http://host.docker.internal:11434/v1'
    try {
      const res = await api<any>(`/api/setup/ollama/models?base_url=${encodeURIComponent(baseUrl)}`)
      ollamaModels = res.models || []
    } catch { ollamaModels = [] }
  }

  function selectedBackendsNeedConfig(): boolean {
    return [...selectedBackends].some(id => {
      const def = BACKENDS.find(b => b.id === id)
      return def && def.fields.length > 0
    })
  }

  // --- Step 1: Telegram ---
  let tgEnabled = $state(false)
  let tgToken = $state('')
  let tgChatId = $state('')
  let tgResult = $state<{ ok: boolean; msg: string } | null>(null)
  let tgBotName = $state('')
  let tgValidating = $state(false)
  let tgGettingChatId = $state(false)

  async function validateTelegram() {
    tgValidating = true
    tgResult = null
    tgBotName = ''
    try {
      const d = await api<any>('POST', '/api/setup/telegram/validate', { bot_token: tgToken })
      if (d.ok) {
        tgBotName = d.bot_name || ''
        tgResult = { ok: true, msg: `Bot: @${d.bot_name}` }
      } else {
        tgResult = { ok: false, msg: d.error || 'Invalid token' }
      }
    } catch (e: any) {
      tgResult = { ok: false, msg: e.error || 'Validation failed' }
    }
    tgValidating = false
  }

  async function getTelegramChatId() {
    tgGettingChatId = true
    try {
      const d = await api<any>('POST', '/api/setup/telegram/chatid', { bot_token: tgToken })
      if (d.ok) {
        tgChatId = d.chat_id
        tgResult = { ok: true, msg: `Found: ${d.name || d.chat_id}` }
      } else {
        tgResult = { ok: false, msg: d.error || 'No messages found' }
      }
    } catch (e: any) {
      tgResult = { ok: false, msg: e.error || 'Failed to get chat ID' }
    }
    tgGettingChatId = false
  }

  // --- Step 2: Tiers ---
  interface Preset {
    id: string; name: string; description: string; backend: string
    tiers: { name: string; model: string; priority: number }[]
  }

  let presets = $state<Preset[]>([])
  let selectedPreset = $state('')
  let presetsLoading = $state(false)

  async function loadPresets() {
    presetsLoading = true
    try {
      const d = await api<any>('/api/setup/presets')
      const all: Preset[] = []
      for (const [backend, arr] of Object.entries(d.presets || {})) {
        for (const p of arr as Preset[]) {
          if (selectedBackends.has(backend)) all.push(p)
        }
      }
      presets = all
      if (all.length > 0 && !selectedPreset) selectedPreset = all[0].id
    } catch { presets = [] }
    presetsLoading = false
  }

  // --- Step 3: Done / Apply ---
  let vaultStatus = $state<string>('')
  let vaultIsNew = $state(false)
  let vaultPassword = $state('')
  let vaultPasswordConfirm = $state('')
  let applying = $state(false)
  let claudeAuthDone = $state<boolean | null>(null)

  let vaultPasswordMismatch = $derived(
    vaultIsNew && vaultPasswordConfirm.length > 0 && vaultPassword !== vaultPasswordConfirm
  )

  async function checkDoneStep() {
    // Check vault status — always show vault password if locked
    try {
      const vs = await api<any>('/api/vault/status')
      vaultStatus = vs.status || ''
      vaultIsNew = vs.status === 'not_initialized' || !!vs.first_time
    } catch { vaultStatus = 'locked'; vaultIsNew = true }

    // Check Claude auth for done step hints
    if (selectedBackends.has('claude')) {
      try {
        const r = await api<any>('/api/setup/claude/check')
        claudeAuthDone = !!r.authenticated
      } catch { claudeAuthDone = false }
    }
  }

  async function applySetup() {
    applyError = ''

    // Vault password confirmation check
    if (vaultNeedsPassword && vaultIsNew && vaultPassword !== vaultPasswordConfirm) {
      applyError = 'Vault passwords do not match'
      return
    }

    applying = true
    const backends: Record<string, any> = {}
    for (const id of selectedBackends) {
      if (id === 'claude') continue
      const def = BACKENDS.find(b => b.id === id)!
      backends[id] = {
        base_url: getField(id, 'base_url') || def.defaultURL || '',
        api_key: getField(id, 'api_key') || '',
        default_model: getField(id, 'default_model') || '',
      }
      if (def.auth) backends[id].auth = def.auth
    }

    const body: any = { backends }
    if (tgEnabled && tgToken && tgChatId) {
      body.telegram = { bot_token: tgToken, chat_id: tgChatId }
    }
    if (selectedPreset) body.preset_id = selectedPreset
    if (vaultNeedsPassword && vaultPassword) body.vault_password = vaultPassword

    try {
      const d = await api<any>('POST', '/api/setup/apply', body)
      if (d.ok) {
        localStorage.setItem('alf-welcomed', '1')
        toasts.show('Setup complete', 'success')
        if (d.restart_required) toasts.show('Restart required for Telegram', 'error')
        step = 4 // advance to Get Started page
      }
    } catch (e: any) {
      applyError = e.error || 'Setup failed'
    }
    applying = false
  }

  let vaultNeedsPassword = $derived(
    vaultStatus === 'locked' || vaultStatus === 'not_initialized'
  )

  // Step navigation
  function nextStep() {
    if (step === 0 && !backendConfigPhase && selectedBackendsNeedConfig()) {
      backendConfigPhase = true
      return
    }
    if (step === 0 && backendConfigPhase) {
      backendConfigPhase = false
    }
    if (step === 3) { applySetup(); return }
    if (step === STEPS.length - 1) { open = false; nav.navigateTo('docs:getting-started'); return }
    step++
    if (step === 2) loadPresets()
    if (step === 3) checkDoneStep()
  }

  function prevStep() {
    if (backendConfigPhase && step === 0) {
      backendConfigPhase = false
      return
    }
    if (step > 0) step--
  }

  function skipStep() {
    tgEnabled = false
    step++
    if (step === 2) loadPresets()
    if (step === 3) checkDoneStep()
  }

  // Welcome modal dismiss
  function dismissWelcome() {
    localStorage.setItem('alf-welcomed', '1')
    open = false
    nav.navigateTo('chat')
  }

  function openGettingStarted() {
    localStorage.setItem('alf-welcomed', '1')
    open = false
    nav.navigateTo('docs:getting-started')
  }

  // Can proceed?
  let canNext = $derived(
    step === 0 ? selectedBackends.size > 0 :
    step === 1 ? true :
    step === 2 ? true :
    step === 3 ? (!vaultNeedsPassword || vaultPassword.length > 0) && !vaultPasswordMismatch :
    true
  )

  // Public method to set mode
  export function setMode(m: 'wizard' | 'welcome') { mode = m; step = 0; backendConfigPhase = false; applyError = '' }
</script>

<Modal bind:open onclose={() => open = false}>
  <div class="wizard-container">
  {#if mode === 'welcome'}
    <!-- Welcome modal (setup already complete, first visit) -->
    <div class="wizard-logo">ALF</div>
    <h2>Welcome to your Control Center</h2>
    <p class="welcome-desc">This is your personal command hub — manage schedules, agent teams, tools, and more from here.</p>
    <div class="welcome-steps">
      <div class="welcome-step">
        <span class="welcome-step-num">1</span>
        <span><strong>Say hello in the Chat</strong> — ALF will learn about you through a short onboarding conversation.</span>
      </div>
      <div class="welcome-step">
        <span class="welcome-step-num">2</span>
        <span><strong>Explore the Docs</strong> — check the getting started guide to discover what ALF can do.</span>
      </div>
      <div class="welcome-step">
        <span class="welcome-step-num">3</span>
        <span><strong>Make it yours</strong> — configure tiers, add skills, and connect your services in the Vault.</span>
      </div>
    </div>
    <blockquote class="welcome-quote">
      <p>"Any sufficiently advanced technology is indistinguishable from magic."</p>
      <cite>— Arthur C. Clarke</cite>
    </blockquote>
    <div class="welcome-actions">
      <button class="btn welcome-docs-btn" onclick={openGettingStarted}>
        Read Getting Started
      </button>
      <button class="btn btn-primary welcome-cta" onclick={dismissWelcome}>Let's go</button>
    </div>
  {:else}
    <!-- Setup Wizard -->
    <div class="wizard-logo">ALF</div>
    <h2>Setup Wizard</h2>

    <!-- Stepper -->
    <div class="stepper">
      {#each STEPS as s, i}
        {#if i > 0}<div class="step-line" class:done={i <= step}></div>{/if}
        <div class="step-item">
          <div class="step-dot" class:active={i === step} class:done={i < step}>{i + 1}</div>
          <span class="step-label">{s}</span>
        </div>
      {/each}
    </div>

    <!-- Step 0: Backend Selection -->
    {#if step === 0 && !backendConfigPhase}
      <div class="step-content">
        <p class="step-desc">Select one or more LLM backends to connect.</p>
        <div class="backend-grid">
          {#each BACKENDS as def}
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div class="backend-card" class:selected={selectedBackends.has(def.id)} onclick={() => toggleBackend(def.id)}>
              <h4>{def.name}</h4>
              <p>{def.desc}</p>

              {#if selectedBackends.has(def.id) && def.id === 'claude'}
                <div class="claude-status" onclick={(e) => e.stopPropagation()}>
                  {#if claudeChecking}
                    <span class="test-loading"><Loader2 size={12} class="spin" /> Checking...</span>
                  {:else if claudeAuth === true}
                    <span class="test-ok"><CheckCircle size={12} /> Authenticated</span>
                  {:else if claudeAuth === false}
                    <span class="test-fail"><XCircle size={12} /> Not authenticated</span>
                    <small>Open the <strong>Terminal</strong> tab, type <code>claude</code>, then run <code>/login</code>. Type <code>/exit</code> when done.</small>
                  {/if}
                </div>
              {/if}

              {#if selectedBackends.has(def.id) && def.id === 'codex'}
                <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
                <div class="claude-status" onclick={(e) => e.stopPropagation()}>
                  <span class="test-loading">API key <em>or</em> <code>codex login --device-auth</code> in Terminal</span>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </div>
    {/if}

    <!-- Step 0b: Backend Configuration -->
    {#if step === 0 && backendConfigPhase}
      <div class="step-content">
        <p class="step-desc">Configure your selected backends.</p>
        {#each [...selectedBackends] as bid}
          {@const def = BACKENDS.find(b => b.id === bid)}
          {#if def && def.fields.length > 0}
            <div class="config-section">
              <h4>{def.name}</h4>
              {#each def.fields as f}
                <div class="form-group">
                  <label for="cfg-{bid}-{f.key}">{f.label}</label>
                  <input
                    id="cfg-{bid}-{f.key}"
                    type={f.type || 'text'}
                    class="input"
                    placeholder={f.placeholder}
                    value={getField(bid, f.key) || f.defaultVal || ''}
                    oninput={(e) => setField(bid, f.key, (e.target as HTMLInputElement).value)}
                  />
                </div>
              {/each}
              <div class="test-row">
                <button class="btn btn-sm" onclick={() => testBackend(def)} disabled={testResults[bid]?.loading}>
                  {testResults[bid]?.loading ? 'Testing...' : 'Test'}
                </button>
                {#if testResults[bid] && !testResults[bid].loading}
                  <span class={testResults[bid].ok ? 'test-ok' : 'test-fail'}>
                    {#if testResults[bid].ok}<CheckCircle size={12} />{:else}<XCircle size={12} />{/if}
                    {testResults[bid].msg}
                  </span>
                {/if}
              </div>
              {#if bid === 'ollama' && ollamaModels.length > 0}
                <div class="ollama-models">Models: {ollamaModels.join(', ')}</div>
              {/if}
            </div>
          {/if}
        {/each}
      </div>
    {/if}

    <!-- Step 1: Telegram -->
    {#if step === 1}
      <div class="step-content">
        <p class="step-desc">Connect Telegram to chat with ALF from your phone. This step is optional.</p>
        <label class="tg-toggle">
          <input type="checkbox" bind:checked={tgEnabled} />
          <span>Enable Telegram</span>
        </label>

        {#if tgEnabled}
          <div class="tg-fields">
            <div class="form-group">
              <label for="sw-tg-token">Bot Token</label>
              <input id="sw-tg-token" type="text" class="input" bind:value={tgToken} placeholder="123456789:ABCdef..." />
            </div>
            <div class="form-actions-row">
              <button class="btn btn-sm" onclick={validateTelegram} disabled={!tgToken || tgValidating}>
                {tgValidating ? 'Validating...' : 'Validate'}
              </button>
              {#if tgResult && tgResult.ok}
                <span class="test-ok"><CheckCircle size={12} /> {tgResult.msg}</span>
              {:else if tgResult && !tgResult.ok}
                <span class="test-fail"><XCircle size={12} /> {tgResult.msg}</span>
              {/if}
            </div>

            {#if tgBotName}
              <div class="tg-bot-link">
                <a href="https://t.me/{tgBotName}" target="_blank">Open @{tgBotName} in Telegram</a>
                <span class="form-help"> — send a message, then click Get Chat ID</span>
              </div>
            {/if}

            <div class="form-group">
              <label for="sw-tg-chatid">Chat ID</label>
              <div class="chatid-row">
                <input id="sw-tg-chatid" type="text" class="input" bind:value={tgChatId} placeholder="Your chat ID" />
                <button class="btn btn-sm" onclick={getTelegramChatId} disabled={!tgToken || tgGettingChatId}>
                  {tgGettingChatId ? 'Getting...' : 'Get Chat ID'}
                </button>
              </div>
            </div>

            <small class="form-help">Create a bot via <strong>@BotFather</strong>. After validating, open the bot link above, send a message, then click <em>Get Chat ID</em>.</small>
          </div>
        {/if}
      </div>
    {/if}

    <!-- Step 2: Tiers -->
    {#if step === 2}
      <div class="step-content">
        <p class="step-desc">Choose a tier preset or keep your current configuration.</p>

        {#if presetsLoading}
          <p class="step-desc">Loading presets...</p>
        {:else}
          <div class="preset-list">
            {#each presets as p}
              <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
              <div class="preset-option" class:selected={selectedPreset === p.id} onclick={() => selectedPreset = p.id}>
                <h4>{p.name}</h4>
                <p>{p.description}</p>
                {#if p.tiers?.length}
                  <div class="preset-preview">
                    <table>
                      <thead><tr><th>Tier</th><th>Model</th><th>Priority</th></tr></thead>
                      <tbody>
                        {#each p.tiers as t}
                          <tr><td>{t.name}</td><td>{t.model}</td><td>{t.priority}</td></tr>
                        {/each}
                      </tbody>
                    </table>
                  </div>
                {/if}
              </div>
            {/each}

            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div class="preset-option" class:selected={selectedPreset === ''} onclick={() => selectedPreset = ''}>
              <h4>Keep current tiers</h4>
              <p>{presets.length === 0 ? 'No presets available for your selected backends. Your current tier configuration will be preserved.' : 'Preserve your existing tier configuration.'}</p>
            </div>
          </div>
        {/if}
      </div>
    {/if}

    <!-- Step 3: Done -->
    {#if step === 3}
      <div class="step-content">
        <p class="step-desc">Review your configuration and apply.</p>
        <dl class="recap">
          <dt>Backends</dt>
          <dd>{[...selectedBackends].join(', ') || 'None selected'}</dd>
          <dt>Telegram</dt>
          <dd>{tgEnabled ? 'Enabled' : 'Skipped'}</dd>
          <dt>Tiers</dt>
          <dd>{selectedPreset ? `Preset: ${selectedPreset}` : 'Keep current'}</dd>
        </dl>

        <!-- Claude auth hints -->
        {#if selectedBackends.has('claude')}
          {#if claudeAuthDone === true}
            <div class="apply-info">
              <strong>Claude</strong> — authenticated.
              <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
              Use the <span class="link" onclick={() => { open = false; nav.navigateTo('terminal') }}>Terminal</span> tab to interact with Claude directly.
            </div>
          {:else if claudeAuthDone === false}
            <div class="apply-warning">
              <strong>Claude not authenticated</strong><br>
              <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
              After setup, open the <span class="link" onclick={() => { open = false; nav.navigateTo('terminal') }}>Terminal</span> tab, type <code>claude</code>, then run <code>/login</code> to connect your Anthropic account. Type <code>/exit</code> when done.
            </div>
          {/if}
        {/if}

        <!-- Codex hint -->
        {#if selectedBackends.has('codex') && !getField('codex', 'api_key')}
          <div class="apply-info">
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <strong>OpenAI Codex</strong> — no API key provided.
            After setup, open the <span class="link" onclick={() => { open = false; nav.navigateTo('terminal') }}>Terminal</span> tab and run <code>codex login --device-auth</code> to authenticate with your ChatGPT subscription.
          </div>
        {/if}

        <!-- Vault password -->
        {#if vaultNeedsPassword}
          <div class="vault-inline">
            <label for="sw-vault-pw">Vault Password{vaultIsNew ? ' (new)' : ''}</label>
            <input id="sw-vault-pw" type="password" class="input" bind:value={vaultPassword}
              placeholder={vaultIsNew ? 'Choose a password (min. 8 characters)' : 'Enter your vault password'} />
            {#if vaultIsNew}
              <input type="password" class="input vault-confirm" bind:value={vaultPasswordConfirm} placeholder="Confirm password" />
              {#if vaultPasswordMismatch}
                <p class="pw-mismatch">Passwords do not match</p>
              {/if}
            {/if}
            <p class="form-help">
              {#if vaultIsNew}
                This creates your encrypted vault for API keys, tokens, and secrets.
                <br><strong class="pw-warning">Remember this password! If lost, the entire vault must be reset.</strong>
              {:else}
                Unlock your vault to store secrets.
              {/if}
            </p>
          </div>
        {/if}

        <!-- Mobile Access -->
        <div class="apply-info coming-soon">
          <strong>Mobile Access</strong> — coming soon<br>
          <span class="form-help">Generate a persistent API token in <strong>Vault &gt; Mobile Access</strong> to connect the ALF mobile app.</span>
        </div>
      </div>
    {/if}

    <!-- Step 4: Get Started -->
    {#if step === 4}
      <div class="step-content" style="text-align:center">
        <h3>You're all set</h3>
        <p class="step-desc">ALF is configured and ready. Read the Getting Started guide to learn the basics.</p>
        <div class="step-notice">
          <strong>Firewall</strong> — The network firewall starts in <em>log-only</em> mode. All outbound requests are logged but not blocked. To enforce rules, go to <strong>Firewall</strong> and switch to <em>enforce</em> mode with an allowlist.
        </div>
      </div>
    {/if}

    <!-- Error display -->
    {#if applyError}
      <div class="apply-error">{applyError}</div>
    {/if}

    <!-- Navigation -->
    <div class="wizard-nav">
      {#if step > 0 || backendConfigPhase}
        <button class="btn" onclick={prevStep}>
          <ChevronLeft size={14} /> Back
        </button>
      {:else}
        <div></div>
      {/if}
      <div class="wizard-nav-right">
        {#if step === 1}
          <button class="btn" onclick={skipStep}>
            <SkipForward size={14} /> Skip
          </button>
        {/if}
        <button class="btn btn-primary" onclick={nextStep} disabled={!canNext || applying}>
          {#if step === 3}
            {applying ? 'Applying...' : 'Apply & Start'}
          {:else if step === STEPS.length - 1}
            Open Getting Started <ChevronRight size={14} />
          {:else}
            Next <ChevronRight size={14} />
          {/if}
        </button>
      </div>
    </div>
  {/if}
  </div>
</Modal>

<style>
  .wizard-container {
    width: 560px;
    max-width: 88vw;
  }

  .wizard-logo {
    text-align: center;
    font-family: 'Sora', sans-serif;
    font-size: var(--font-xl, 24px);
    font-weight: 700;
    letter-spacing: 0.1em;
    color: var(--accent);
    margin-bottom: 8px;
  }

  h2 {
    font-size: var(--font-lg, 18px);
    font-weight: 600;
    margin: 0 0 20px;
    text-align: center;
  }

  /* Welcome modal */
  .welcome-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    text-align: center;
    margin-bottom: 20px;
  }

  .welcome-steps {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-bottom: 24px;
  }

  .welcome-step {
    display: flex;
    gap: 12px;
    align-items: flex-start;
    font-size: var(--font-sm, 13px);
  }

  .welcome-step-num {
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: 28px;
    height: 28px;
    border-radius: 50%;
    background: var(--accent);
    color: var(--on-accent);
    font-weight: 600;
    font-size: var(--font-sm, 13px);
    flex-shrink: 0;
  }

  .step-notice {
    margin: 16px 0 0;
    padding: 10px 14px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    line-height: 1.5;
    text-align: left;
  }

  .welcome-quote {
    margin: 0 0 20px;
    padding: 12px 16px;
    border-left: 3px solid var(--accent);
    background: var(--bg-input);
    border-radius: 0 var(--radius, 8px) var(--radius, 8px) 0;
  }

  .welcome-quote p {
    margin: 0 0 4px;
    font-size: var(--font-sm, 13px);
    font-style: italic;
    color: var(--text);
    line-height: 1.5;
  }

  .welcome-quote cite {
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    font-style: normal;
  }

  .welcome-actions {
    display: flex;
    gap: 10px;
  }

  .welcome-docs-btn {
    flex: 1;
    justify-content: center;
    padding: 10px;
    font-size: var(--font-sm, 13px);
  }

  .welcome-cta {
    flex: 1;
    justify-content: center;
    padding: 10px;
    font-size: var(--font-sm, 13px);
  }

  /* Stepper */
  .stepper {
    display: flex;
    align-items: center;
    gap: 0;
    margin-bottom: 24px;
    padding: 0 8px;
  }

  .step-item {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
  }

  .step-dot {
    width: 28px;
    height: 28px;
    border-radius: 50%;
    border: 2px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: var(--font-xs, 11px);
    font-weight: 600;
    color: var(--text-dim);
    background: var(--bg-card);
    transition: all 0.2s;
  }

  .step-dot.active { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
  .step-dot.done { background: var(--accent); color: var(--on-accent); border-color: var(--accent); opacity: 0.6; }

  .step-line {
    flex: 1;
    height: 2px;
    background: var(--border);
    transition: background 0.2s;
    min-width: 24px;
  }

  .step-line.done { background: var(--accent); opacity: 0.5; }

  .step-label {
    position: absolute;
    top: 32px;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    white-space: nowrap;
  }

  .step-content {
    margin-top: 16px;
    min-height: 200px;
  }

  .step-desc {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin: 0 0 12px;
  }

  /* Backend grid */
  .backend-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
    gap: 10px;
  }

  .backend-card {
    border: 2px solid var(--border);
    border-radius: var(--radius, 8px);
    padding: 12px;
    cursor: pointer;
    transition: border-color 0.15s, background 0.15s;
  }

  .backend-card:hover { border-color: var(--text-dim); }
  .backend-card.selected { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 8%, var(--bg-card)); }
  .backend-card h4 { margin: 0 0 2px; font-size: var(--font-sm, 13px); }
  .backend-card p { margin: 0; font-size: var(--font-xs, 11px); color: var(--text-dim); }

  /* Config sections (phase 2) */
  .config-section {
    margin-bottom: 16px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--border);
  }

  .config-section:last-child { border-bottom: none; }
  .config-section h4 { margin: 0 0 8px; font-size: var(--font-sm, 13px); }

  .config-section .form-group { margin-bottom: 8px; }
  .config-section .form-group label {
    display: block;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    margin-bottom: 3px;
  }
  .config-section .form-group input { width: 100%; box-sizing: border-box; }

  .test-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 6px;
  }

  .test-ok { font-size: var(--font-xs, 11px); color: var(--accent); display: inline-flex; align-items: center; gap: 4px; }
  .test-fail { font-size: var(--font-xs, 11px); color: var(--red, #e55); display: inline-flex; align-items: center; gap: 4px; }
  .test-loading { font-size: var(--font-xs, 11px); color: var(--text-dim); display: inline-flex; align-items: center; gap: 4px; }

  .claude-status { margin-top: 8px; }
  .claude-status small { display: block; margin-top: 4px; font-size: var(--font-xs, 11px); color: var(--text-dim); }
  .claude-status code, code { font-size: var(--font-xs, 11px); padding: 1px 4px; border-radius: 3px; background: var(--bg-input); }

  .ollama-models { margin-top: 6px; font-size: var(--font-xs, 11px); color: var(--text-dim); }

  /* Telegram */
  .tg-toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    margin-bottom: 12px;
  }

  .tg-fields { margin-top: 8px; }

  .form-actions-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 12px;
  }

  .form-help { font-size: var(--font-xs, 11px); color: var(--text-dim); margin-top: 4px; }
  .form-help a, .link { color: var(--accent); cursor: pointer; text-decoration: none; }
  .form-help a:hover, .link:hover { text-decoration: underline; }

  .tg-bot-link { margin: 8px 0; font-size: var(--font-sm, 13px); }
  .tg-bot-link a { color: var(--accent); }

  .chatid-row { display: flex; gap: 8px; align-items: center; }
  .chatid-row input { flex: 1; }

  /* Presets */
  .preset-list { display: flex; flex-direction: column; gap: 10px; }

  .preset-option {
    border: 2px solid var(--border);
    border-radius: var(--radius, 8px);
    padding: 12px;
    cursor: pointer;
    transition: border-color 0.15s;
  }

  .preset-option:hover { border-color: var(--text-dim); }
  .preset-option.selected { border-color: var(--accent); }
  .preset-option h4 { margin: 0; font-size: var(--font-sm, 13px); }
  .preset-option p { font-size: var(--font-xs, 11px); color: var(--text-dim); margin: 4px 0 0; }

  .preset-preview { margin-top: 8px; }
  .preset-preview table { width: 100%; border-collapse: collapse; font-size: var(--font-xs, 11px); }
  .preset-preview th {
    text-align: left;
    font-family: 'JetBrains Mono', monospace;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    padding: 3px 8px 3px 0;
    border-bottom: 1px solid var(--border);
  }
  .preset-preview td { padding: 3px 8px 3px 0; font-family: 'JetBrains Mono', monospace; }

  /* Recap */
  .recap { margin: 0 0 16px; }
  .recap dt { font-size: var(--font-xs, 11px); text-transform: uppercase; letter-spacing: 0.05em; color: var(--text-dim); margin-top: 8px; }
  .recap dd { font-size: var(--font-sm, 13px); margin: 2px 0 0; }

  .apply-info {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    padding: 8px 12px;
    background: var(--bg);
    border-radius: var(--radius, 8px);
    margin-bottom: 8px;
  }

  .coming-soon {
    opacity: 0.6;
    border-style: dashed;
  }

  .apply-warning {
    font-size: var(--font-sm, 13px);
    color: var(--yellow);
    padding: 8px 12px;
    border: 1px solid var(--yellow);
    border-radius: var(--radius, 8px);
    margin-bottom: 8px;
    background: color-mix(in srgb, var(--yellow) 5%, transparent);
  }

  .vault-inline {
    background: var(--bg);
    border-radius: var(--radius, 8px);
    padding: 12px;
    margin-top: 12px;
  }

  .vault-inline label {
    display: block;
    font-size: var(--font-sm, 13px);
    font-weight: 500;
    margin-bottom: 6px;
  }

  .vault-confirm { margin-top: 6px; }

  .pw-mismatch { color: var(--red); font-size: var(--font-xs, 11px); margin: 4px 0 0; }
  .pw-warning { color: var(--red); }

  .apply-error {
    margin-top: 8px;
    padding: 8px 12px;
    border-radius: var(--radius, 8px);
    font-size: var(--font-sm, 13px);
    background: color-mix(in srgb, var(--red) 10%, transparent);
    color: var(--red);
  }

  /* Navigation */
  .wizard-nav {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-top: 24px;
    padding-top: 16px;
    border-top: 1px solid var(--border);
  }

  .wizard-nav-right { display: flex; gap: 8px; }

  :global(.spin) { animation: spin 1s linear infinite; }
  @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

</style>
