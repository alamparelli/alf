<script lang="ts">
  import { onMount } from 'svelte'
  import { RotateCw, CheckCircle, CircleOff, Pencil, Bug, Settings2 } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import { theme, ALF_THEMES } from '../stores/theme.svelte'
  import { toasts } from '../stores/toast.svelte'
  import { sound } from '../stores/sound.svelte'
  import { spotlightSettings } from '../stores/spotlight.svelte'
  import { api, esc, waitForDaemonAndReload } from '../lib/api'

  // --- Spotlight shortcut ---
  let shortcutCapturing = $state(false)
  let shortcutDisplay = $derived(spotlightSettings.shortcutKey.toUpperCase())

  function startCapture() {
    shortcutCapturing = true
  }

  function onShortcutKeydown(e: KeyboardEvent) {
    if (!shortcutCapturing) return
    e.preventDefault()
    const key = e.key
    // Ignore modifier-only keys
    if (['Meta', 'Control', 'Alt', 'Shift', 'Tab', 'Escape'].includes(key)) return
    if (key.length === 1) {
      spotlightSettings.setKey(key)
    }
    shortcutCapturing = false
  }

  // --- Theme ---
  function onThemeChange(e: Event) {
    const select = e.target as HTMLSelectElement
    theme.apply(select.value)
  }

  // --- Restart ---
  let restartOutput = $state('')
  let showOutput = $state(false)

  async function restart() {
    if (!confirm('Restart the ALF daemon?')) return
    try {
      await api('POST', '/api/restart')
    } catch {
      // expected
    }
    showOutput = true
    waitForDaemonAndReload((msg) => { restartOutput = msg })
  }

  // --- Telegram ---
  let tgConfigured = $state(false)
  let tgBotName = $state('')
  let tgChatId = $state('')
  let tgTokenMasked = $state('')
  let tgToken = $state('')
  let tgChatInput = $state('')
  let tgExpanded = $state(false)
  let tgSaving = $state(false)
  let tgResult = $state('')
  let tgResultType = $state<'success' | 'error'>('success')

  async function loadTelegram() {
    try {
      const data = await api<any>('/api/telegram')
      tgConfigured = !!data.configured
      tgBotName = data.bot_name || ''
      tgChatId = data.chat_id || ''
      tgTokenMasked = data.bot_token_masked || '***'
      tgChatInput = data.chat_id || ''
      tgExpanded = !data.configured
    } catch {
      tgExpanded = true
    }
  }

  async function saveTelegram() {
    if (!tgToken.trim() || !tgChatInput.trim()) {
      tgResult = 'Both bot token and chat ID are required.'
      tgResultType = 'error'
      return
    }
    tgSaving = true
    try {
      const data = await api<any>('/api/telegram', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ bot_token: tgToken, chat_id: tgChatInput })
      })
      tgResult = 'Telegram connected to @' + (data.bot_name || '') + '. Restart ALF to activate.'
      tgResultType = 'success'
      loadTelegram()
    } catch (e: any) {
      tgResult = e.error || e.message || 'Failed to save Telegram config.'
      tgResultType = 'error'
    } finally {
      tgSaving = false
    }
  }

  async function disconnectTelegram() {
    if (!confirm('Disconnect Telegram? ALF will run in Control Center-only mode after restart.')) return
    try {
      await api('/api/telegram', { method: 'DELETE' })
      tgResult = 'Telegram disconnected. Restart ALF to apply.'
      tgResultType = 'success'
      loadTelegram()
    } catch {
      tgResult = 'Failed to disconnect.'
      tgResultType = 'error'
    }
  }

  // --- Updates ---
  let autoUpdateCheck = $state(true)
  let autoUpdateNotify = $state(false)

  async function persistUpdateSettings() {
    try {
      const cfg = await api<any>('GET', '/api/config')
      cfg.auto_update_check = autoUpdateCheck
      cfg.auto_update_notify = autoUpdateNotify
      delete cfg.backends
      await api('PUT', '/api/config', cfg)
    } catch {
      // silent
    }
  }

  // --- Version ---
  let version = $state('')
  let updateAvailable = $state('')

  onMount(async () => {
    loadTelegram()
    try {
      const [status, cfg] = await Promise.all([
        api<any>('/api/status'),
        api<any>('/api/config'),
      ])
      version = status.version || ''
      updateAvailable = status.update_available || ''
      autoUpdateCheck = cfg.auto_update_check !== false
      autoUpdateNotify = !!cfg.auto_update_notify
    } catch {
      // silent
    }
  })
</script>

<div class="view-layout">
  <h2>Settings</h2>

  {#if updateAvailable}
    <div class="update-banner">
      <span>Update available: <strong>{updateAvailable}</strong></span>
      <span class="update-current">Current: {version}</span>
    </div>
  {/if}

  <!-- Theme -->
  <Card>
    <div class="row">
      <h3>Theme</h3>
      <select class="settings-select" value={theme.palette} onchange={onThemeChange}>
        {#each Object.entries(ALF_THEMES) as [key, t]}
          <option value={key}>{t.label}</option>
        {/each}
      </select>
      <span class="hint">Light/dark follows your system.</span>
    </div>
  </Card>

  <!-- Notifications -->
  <Card>
    <div class="row">
      <h3>Notifications</h3>
    </div>
    <div class="notif-row">
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={sound.enabled} onchange={() => sound.persist()} />
        <span class="slider"></span>
        <span class="toggle-text">Chat sound</span>
      </label>
      <span class="hint">Play a sound when a chat message arrives</span>
    </div>
    <div class="notif-row">
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={autoUpdateCheck} onchange={persistUpdateSettings} />
        <span class="slider"></span>
        <span class="toggle-text">Check for updates</span>
      </label>
      <span class="hint">Periodically check if a newer version is available</span>
    </div>
    <div class="notif-row">
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={autoUpdateNotify} onchange={persistUpdateSettings} disabled={!autoUpdateCheck} />
        <span class="slider"></span>
        <span class="toggle-text">Notify on update</span>
      </label>
      <span class="hint">Send a Telegram notification when an update is available</span>
    </div>
  </Card>

  <!-- Shortcuts -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <Card>
    <div class="row">
      <h3>Shortcuts</h3>
    </div>
    <div class="notif-row">
      <span class="shortcut-label">Spotlight</span>
      <span class="hint">{navigator?.platform?.includes('Mac') ? '⌘' : 'Ctrl'}+</span>
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <button
        class="shortcut-key-btn"
        class:capturing={shortcutCapturing}
        onclick={startCapture}
        onkeydown={onShortcutKeydown}
        onblur={() => shortcutCapturing = false}
        title="Click then press a key"
      >
        {shortcutCapturing ? '...' : shortcutDisplay}
      </button>
      <span class="hint">{shortcutCapturing ? 'Press a key' : 'Click to change'}</span>
    </div>
  </Card>

  <!-- System -->
  <Card>
    <div class="row">
      <h3>System</h3>
      <button class="btn btn-sm" onclick={restart}>
        <RotateCw size={13} /> Restart
      </button>
      <button class="btn btn-sm" onclick={() => window.dispatchEvent(new CustomEvent('alf:open-wizard'))}>
        <Settings2 size={13} /> Re-run Setup Wizard
      </button>
    </div>
    {#if showOutput}
      <div class="admin-output">
        <pre>{restartOutput}</pre>
      </div>
    {/if}
  </Card>

  <!-- Telegram -->
  <Card>
    <div class="tg-header">
      <h3>Telegram Integration</h3>
      {#if tgConfigured && !tgExpanded}
        <button class="btn btn-sm" onclick={() => tgExpanded = true}>
          <Pencil size={13} /> Edit
        </button>
      {/if}
    </div>

    <!-- Status -->
    {#if tgConfigured}
      <div class="tg-status tg-connected">
        <CheckCircle size={14} />
        Connected
      </div>
    {:else}
      <div class="tg-status tg-disconnected">
        <CircleOff size={14} /> Not configured
      </div>
    {/if}

    <!-- Form -->
    {#if tgExpanded}
      <div class="tg-form">
        <p class="form-hint">Connect Telegram to chat with ALF from your phone.</p>
        <div class="form-group">
          <label for="tgBotToken">Bot Token</label>
          <input id="tgBotToken" type="text" bind:value={tgToken} placeholder={tgTokenMasked || '123456789:ABCdef...'} class="input" />
          <small class="form-help">Create a bot via <a href="https://t.me/BotFather" target="_blank">@BotFather</a></small>
        </div>
        <div class="form-group">
          <label for="tgChatID">Chat ID</label>
          <input id="tgChatID" type="text" bind:value={tgChatInput} placeholder="123456789" class="input" />
          <small class="form-help">Your personal chat ID</small>
        </div>
        <div class="form-actions">
          <button class="btn btn-primary" onclick={saveTelegram} disabled={tgSaving}>
            {tgSaving ? 'Verifying...' : 'Save & Verify'}
          </button>
          {#if tgConfigured}
            <button class="btn" onclick={() => { tgExpanded = false; tgResult = '' }}>Cancel</button>
            <button class="btn btn-sm" onclick={disconnectTelegram}>Disconnect</button>
          {/if}
        </div>
        {#if tgResult}
          <div class="tg-result {tgResultType}">{tgResult}</div>
        {/if}
      </div>
    {/if}
  </Card>

  <!-- About -->
  <Card>
    <h3>About</h3>
    {#if version}
      <p class="about-version">{version}</p>
    {/if}
    <p class="about-love">Made with &hearts;</p>
    <div class="about-socials">
      <a href="https://x.com/a_lamparelli" target="_blank" title="X / Twitter">
        <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>
      </a>
      <a href="https://github.com/alamparelli" target="_blank" title="GitHub">
        <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><path d="M12 0C5.37 0 0 5.37 0 12c0 5.3 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61-.546-1.385-1.335-1.755-1.335-1.755-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.605-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 21.795 24 17.295 24 12c0-6.63-5.37-12-12-12z"/></svg>
      </a>
    </div>
    <span class="about-label">Creator of:</span>
    <div class="about-links">
      <a href="https://dicta.to?utm_source=alf&utm_medium=bio&utm_campaign=generic" target="_blank">Dictato</a>
      <a href="https://quickpoll.cc?utm_source=alf&utm_medium=bio&utm_campaign=generic" target="_blank">QuickPoll</a>
      <a href="https://contacthive.app?utm_source=alf&utm_medium=bio&utm_campaign=generic" target="_blank">ContactHive</a>
    </div>
    <div class="about-actions">
      <a href="https://x.com/intent/tweet?text=Powered%20by%20ALF%20OS" target="_blank" rel="noopener" class="btn btn-sm">
        <svg viewBox="0 0 24 24" width="14" height="14" fill="currentColor" style="vertical-align:-2px"><path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117z"/></svg>
        Share on X
      </a>
      <a href="mailto:al@obsidian-it.be?subject=ALF%20Bug%20Report" class="btn btn-sm">
        <Bug size={13} /> Report Issue
      </a>
    </div>
  </Card>
</div>

<style>
  .settings-view h2 {
    margin-bottom: 16px;
  }

  .update-banner {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 16px;
    margin-bottom: 16px;
    background: rgba(234, 179, 8, 0.1);
    border: 1px solid var(--yellow, #eab308);
    border-radius: var(--radius, 8px);
    font-size: 0.85rem;
    color: var(--text);
  }

  .update-current {
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    flex-wrap: wrap;
  }

  .row h3 {
    margin: 0;
  }

  .settings-select {
    padding: 4px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
  }

  .hint {
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  .shortcut-label {
    font-size: 0.85rem;
    font-weight: 500;
  }

  .shortcut-key-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 28px;
    padding: 2px 8px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 4px;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--text);
    cursor: pointer;
    transition: border-color 0.15s;
  }

  .shortcut-key-btn:hover {
    border-color: var(--accent);
  }

  .shortcut-key-btn.capturing {
    border-color: var(--accent);
    color: var(--accent);
    outline: none;
  }

  .notif-row {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-top: 8px;
    flex-wrap: wrap;
  }

  .toggle-switch {
    display: flex;
    align-items: center;
    gap: 10px;
    cursor: pointer;
    font-size: 0.85rem;
    position: relative;
  }

  .toggle-switch input {
    position: absolute;
    opacity: 0;
    width: 0;
    height: 0;
  }

  .slider {
    width: 36px;
    height: 20px;
    background: var(--border);
    border-radius: 10px;
    position: relative;
    transition: background 0.2s;
    flex-shrink: 0;
  }

  .slider::after {
    content: '';
    position: absolute;
    top: 2px;
    left: 2px;
    width: 16px;
    height: 16px;
    background: var(--bg-card, #fff);
    border-radius: 50%;
    transition: transform 0.2s;
  }

  .toggle-switch input:checked + .slider {
    background: var(--accent);
  }

  .toggle-switch input:checked + .slider::after {
    transform: translateX(16px);
  }

  .toggle-text {
    user-select: none;
  }

  .toggle-switch input:disabled + .slider {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .admin-output {
    margin-top: 12px;
    padding: 8px 12px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.8rem;
    color: var(--text-dim);
  }

  .admin-output pre {
    white-space: pre-wrap;
    word-break: break-word;
  }

  /* Telegram */
  .tg-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .tg-status {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.85rem;
    margin-bottom: 8px;
  }

  .tg-connected {
    color: var(--green);
  }

  .tg-disconnected {
    color: var(--text-dim);
  }

  .tg-form {
    margin-top: 8px;
  }

  .form-hint {
    font-size: 0.82rem;
    color: var(--text-dim);
    margin-bottom: 12px;
  }

  .form-help {
    display: block;
    font-size: 0.72rem;
    color: var(--text-dim);
    margin-top: 4px;
  }

  .form-help a {
    color: var(--accent);
  }

  .form-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .tg-result {
    margin-top: 10px;
    padding: 8px 12px;
    border-radius: var(--radius, 8px);
    font-size: 0.82rem;
  }

  .tg-result.success {
    background: rgba(61, 139, 61, 0.1);
    color: var(--green);
  }

  .tg-result.error {
    background: rgba(196, 57, 42, 0.1);
    color: var(--red);
  }

  /* About */
  .about-version {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.8rem;
    color: var(--text-dim);
    margin-bottom: 4px;
  }

  .about-love {
    font-size: 0.85rem;
    color: var(--text-dim);
    margin-bottom: 12px;
  }

  .about-socials {
    display: flex;
    gap: 12px;
    margin-bottom: 12px;
  }

  .about-socials a {
    color: var(--text-dim);
    transition: color 0.15s;
  }

  .about-socials a:hover {
    color: var(--text);
  }

  .about-label {
    font-size: 0.75rem;
    color: var(--text-dim);
    display: block;
    margin-bottom: 4px;
  }

  .about-links {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
    margin-bottom: 12px;
  }

  .about-links a {
    color: var(--accent);
    text-decoration: none;
    font-size: 0.82rem;
    font-weight: 500;
  }

  .about-links a:hover {
    text-decoration: underline;
  }

  .about-actions {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
  }
</style>
