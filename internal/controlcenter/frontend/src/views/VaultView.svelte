<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { Lock, Unlock, Shield, Key, FileText, Plus, Trash2, Download, Upload, Eye, EyeOff, Copy, RefreshCw, AlertTriangle, CheckCircle, ExternalLink, Loader2, Zap, Pencil, Info, Terminal } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import Toggle from '../components/shared/Toggle.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'

  // Vault status
  let available = $state(false)
  let vaultStatus = $state('') // 'sealed', 'active', 'unreachable'
  let firstTime = $state(false)
  let loading = $state(true)
  let password = $state('')
  let unlocking = $state(false)

  // Services
  interface Service {
    name: string
    base_url: string
    auth_type: string
    tls_skip_verify?: boolean
    session_cookies?: boolean
    scopes?: string[]
    expires_at?: string
    token_status?: string
  }
  let services = $state<Service[]>([])

  // Secrets
  interface Secret {
    name: string
    set: boolean
  }
  let secrets = $state<Secret[]>([])
  let vaultStorageTab = $state<'secrets' | 'files'>('secrets')

  // Files
  interface VaultFile {
    name: string
    size?: number
  }
  let files = $state<VaultFile[]>([])

  // Access keys
  interface AccessToken {
    id: string
    scope: string
    created_at?: string
    expires_at?: string
  }
  let tokens = $state<AccessToken[]>([])

  // Service modal
  let showServiceModal = $state(false)
  let editingService = $state<Service | null>(null)
  let svcName = $state('')
  let svcBaseUrl = $state('')
  let svcAuthType = $state('bearer')
  let svcToken = $state('')
  let svcTokenRef = $state('')
  let svcUseRef = $state(false)
  let svcHeaderName = $state('')
  let svcHeaderValue = $state('')
  let svcHeaderValueRef = $state('')
  let svcHeaderUseRef = $state(false)
  let svcUsername = $state('')
  let svcPassword = $state('')
  let svcPasswordRef = $state('')
  let svcBasicUseRef = $state(false)
  let svcClientId = $state('')
  let svcClientSecret = $state('')
  let svcTokenUrl = $state('')
  let svcRefreshToken = $state('')
  let svcScopes = $state('')
  let svcOAuthFileRef = $state('')
  let svcOAuthBrowserScopes = $state('')
  let svcOAuthTab = $state('browser') // 'browser' or 'manual'
  let svcSaFileRef = $state('')
  let svcSaScopes = $state('')
  let svcSaTokenUrl = $state('')
  // SSH key
  let svcSshHost = $state('')
  let svcSshPort = $state(22)
  let svcSshUser = $state('')
  let svcSshKeyFileRef = $state('')
  let svcSshPassphrase = $state('')
  let svcTlsSkip = $state(false)
  let svcSessionCookies = $state(false)
  let savingService = $state(false)

  // OAuth2 flow
  let oauthPolling = $state(false)
  let oauthPollTimer: ReturnType<typeof setInterval> | undefined

  // Secret add/edit
  let showSecretModal = $state(false)
  let secretName = $state('')
  let secretValue = $state('')
  let savingSecret = $state(false)

  // File upload
  let fileInput: HTMLInputElement | undefined
  let uploadingFile = $state(false)

  // Built-in tokens (from status)
  let adminToken = $state('')
  let proxyToken = $state('')

  // Token creation
  let tokenScope = $state<'proxy' | 'admin'>('proxy')
  let newTokenId = $state('')
  let showTokenModal = $state(false)

  // Export/Import
  let exportPassword = $state('')
  let importPassword = $state('')
  let importFileInput: HTMLInputElement | undefined

  // Mobile API token
  let mobileTokenExists = $state(false)
  let mobileTokenMasked = $state('')
  let mobileTokenFull = $state('')
  let mobileTokenCopied = $state(false)
  let mobileGenerating = $state(false)

  let isUnlocked = $derived(vaultStatus === 'unlocked')

  async function loadStatus() {
    // Only show loading spinner on first load
    if (!vaultStatus) loading = true
    try {
      const data = await api<any>('/api/vault/status')
      available = data.available
      vaultStatus = data.status
      firstTime = data.first_time
      adminToken = data.admin_token || ''
      proxyToken = data.proxy_token || ''
    } catch {
      available = false
    } finally {
      loading = false
    }
  }

  async function unlock() {
    if (!password) return
    unlocking = true
    try {
      await api('/api/vault/unlock', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password })
      })
      password = ''
      toasts.show('Vault unlocked', 'success')
      // Reload status without setting loading=true (avoids flash)
      const data = await api<any>('/api/vault/status')
      available = data.available
      vaultStatus = data.status
      firstTime = data.first_time
      adminToken = data.admin_token || ''
      proxyToken = data.proxy_token || ''
      loading = false
      if (vaultStatus === 'unlocked') loadAll()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to unlock vault', 'error')
    } finally {
      unlocking = false
    }
  }

  async function lockVault() {
    if (!confirm('Lock the vault? Services will be unavailable until you unlock again.')) return
    try {
      await api('/api/vault/lock', { method: 'POST' })
      toasts.show('Vault locked', 'success')
      services = []
      secrets = []
      files = []
      tokens = []
      loadStatus()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to lock', 'error')
    }
  }

  async function loadAll() {
    loadServices()
    loadSecrets()
    loadFiles()
    loadTokens()
    loadMobileToken()
  }

  async function loadMobileToken() {
    try {
      const d = await api<any>('/api/vault/mobile-token')
      mobileTokenExists = !!d.exists
      mobileTokenMasked = d.token_masked || ''
      mobileTokenFull = '' // never persisted in frontend
    } catch { /* silent */ }
  }

  async function generateMobileToken() {
    mobileGenerating = true
    mobileTokenCopied = false
    try {
      const d = await api<any>('POST', '/api/vault/mobile-token', {})
      if (d.ok) {
        mobileTokenExists = true
        mobileTokenFull = d.token
        mobileTokenMasked = ''
        toasts.show('Mobile token generated', 'success')
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to generate token', 'error')
    }
    mobileGenerating = false
  }

  async function revokeMobileToken() {
    if (!confirm('Revoke mobile API token? The mobile app will lose access.')) return
    try {
      await api('/api/vault/mobile-token', { method: 'DELETE' })
      mobileTokenExists = false
      mobileTokenFull = ''
      mobileTokenMasked = ''
      toasts.show('Mobile token revoked', 'success')
    } catch (e: any) {
      toasts.show(e.error || 'Failed to revoke token', 'error')
    }
  }

  async function copyMobileToken() {
    try {
      await navigator.clipboard.writeText(mobileTokenFull)
      mobileTokenCopied = true
      setTimeout(() => { mobileTokenCopied = false }, 2000)
    } catch {
      toasts.show('Failed to copy', 'error')
    }
  }

  // --- Services ---
  async function loadServices() {
    try {
      const data = await api<Service[]>('/api/vault/services')
      services = (data || []).sort((a, b) => a.name.localeCompare(b.name))
    } catch { /* silent */ }
  }

  function splitScopes(s: string): string[] {
    return s.split(/[,\s]+/).map(x => x.trim()).filter(Boolean)
  }

  function openServiceModal(svc?: Service) {
    editingService = svc || null
    svcName = svc?.name || ''
    svcBaseUrl = svc?.base_url || ''
    svcAuthType = svc?.auth_type || 'bearer'
    svcTlsSkip = svc?.tls_skip_verify || false
    svcSessionCookies = svc?.session_cookies || false
    svcToken = ''
    svcTokenRef = ''
    svcUseRef = false
    svcHeaderName = ''
    svcHeaderValue = ''
    svcHeaderValueRef = ''
    svcHeaderUseRef = false
    svcUsername = ''
    svcPassword = ''
    svcPasswordRef = ''
    svcBasicUseRef = false
    svcClientId = ''
    svcClientSecret = ''
    svcTokenUrl = ''
    svcRefreshToken = ''
    const scopeStr = (svc?.scopes || []).join(', ')
    svcScopes = scopeStr
    svcOAuthFileRef = ''
    svcOAuthBrowserScopes = scopeStr
    svcOAuthTab = 'browser'
    svcSaFileRef = ''
    svcSaScopes = scopeStr
    svcSaTokenUrl = ''
    showServiceModal = true
  }

  async function saveService() {
    if (!svcName.trim()) {
      toasts.show('Name is required', 'error')
      return
    }
    if (svcAuthType !== 'ssh_key' && !svcBaseUrl.trim()) {
      toasts.show('Base URL is required', 'error')
      return
    }
    savingService = true
    try {
      const auth: Record<string, any> = { type: svcAuthType }
      switch (svcAuthType) {
        case 'bearer':
          if (svcUseRef && svcTokenRef) auth.token_ref = svcTokenRef
          else auth.token = svcToken
          break
        case 'header':
          auth.header_name = svcHeaderName
          if (svcHeaderUseRef && svcHeaderValueRef) auth.header_value_ref = svcHeaderValueRef
          else auth.header_value = svcHeaderValue
          break
        case 'url':
          auth.token = svcToken
          break
        case 'basic':
          auth.username = svcUsername
          if (svcBasicUseRef && svcPasswordRef) auth.password_ref = svcPasswordRef
          else auth.password = svcPassword
          break
        case 'oauth2_client':
          if (svcOAuthTab === 'browser' && svcOAuthFileRef) {
            auth.client_secret_file = svcOAuthFileRef
            auth.scopes = splitScopes(svcOAuthBrowserScopes)
          } else {
            auth.client_id = svcClientId
            auth.client_secret = svcClientSecret
            auth.token_url = svcTokenUrl
            if (svcRefreshToken) auth.refresh_token = svcRefreshToken
            auth.scopes = splitScopes(svcScopes)
          }
          break
        case 'service_account':
          auth.file_ref = svcSaFileRef
          auth.sa_scopes = splitScopes(svcSaScopes)
          if (svcSaTokenUrl) auth.sa_token_url = svcSaTokenUrl
          break
        case 'ssh_key':
          auth.ssh_host = svcSshHost.trim()
          auth.ssh_port = svcSshPort || 22
          auth.ssh_user = svcSshUser.trim()
          auth.ssh_key_file_ref = svcSshKeyFileRef
          if (svcSshPassphrase) auth.ssh_key_passphrase = svcSshPassphrase
          break
      }
      const payload: Record<string, any> = {
        name: svcName.trim(),
        auth,
      }
      if (svcAuthType !== 'ssh_key') {
        payload.base_url = svcBaseUrl.trim()
        payload.tls_skip_verify = svcTlsSkip
        payload.session_cookies = svcSessionCookies
      }
      await api('/api/vault/services', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
      toasts.show('Service saved', 'success')
      showServiceModal = false
      loadServices()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to save service', 'error')
    } finally {
      savingService = false
    }
  }

  async function deleteService(name: string) {
    if (!confirm(`Delete service "${name}"?`)) return
    try {
      await api(`/api/vault/services/${encodeURIComponent(name)}`, { method: 'DELETE' })
      toasts.show('Service deleted', 'success')
      loadServices()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to delete', 'error')
    }
  }

  async function testService(name: string) {
    try {
      const data = await api<any>(`/api/vault/services/${encodeURIComponent(name)}/test`, { method: 'POST' })
      if (data.ok) {
        toasts.show(`${name}: connection OK`, 'success')
      } else {
        let msg = data.error || 'test failed'
        // Make common upstream errors more user-friendly.
        if (msg.includes('redirects disabled')) {
          msg = 'Service redirected the request (blocked for security). Check that the Base URL points directly to the API, not a page that redirects.'
        } else if (msg.includes('upstream request failed') || msg.includes('502')) {
          msg = 'Could not reach the service. Check the Base URL is correct and the service is running.'
        } else if (msg.includes('401') || msg.includes('403') || msg.includes('Unauthorized')) {
          msg = 'Authentication rejected. Check your credentials.'
        }
        toasts.show(`${name}: ${msg}`, 'error')
      }
    } catch (e: any) {
      toasts.show(e.error || 'Test failed', 'error')
    }
  }

  // OAuth2 flow (matches legacy vaultOAuth2StartFlow)
  let oauthAttempts = 0
  async function startOAuth2Flow() {
    if (!svcName.trim() || !svcBaseUrl.trim()) { toasts.show('Name and Base URL are required', 'error'); return }
    if (!svcOAuthFileRef) { toasts.show('Select a client secret file', 'error'); return }

    const payload: Record<string, any> = {
      client_secret_file: svcOAuthFileRef,
      service_name: svcName.trim(),
      base_url: svcBaseUrl.trim(),
      redirect_uri: window.location.origin + '/api/vault/oauth2/callback',
    }
    const scopes = splitScopes(svcOAuthBrowserScopes)
    if (scopes.length > 0) payload.scopes = scopes
    if (svcTlsSkip) payload.tls_skip_verify = true
    if (svcSessionCookies) payload.session_cookies = true

    try {
      const data = await api<any>('/api/vault/oauth2/authorize', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (data.auth_url) {
        window.open(data.auth_url, '_blank')
        oauthPolling = true
        oauthAttempts = 0
        // Poll for service creation (5 min timeout like legacy)
        oauthPollTimer = setInterval(async () => {
          oauthAttempts++
          if (oauthAttempts > 60) {
            clearInterval(oauthPollTimer)
            oauthPolling = false
            toasts.show('OAuth2 flow timed out', 'error')
            return
          }
          try {
            const svcList = await api<Service[]>('/api/vault/services')
            if ((svcList || []).some(s => s.name === svcName.trim())) {
              clearInterval(oauthPollTimer)
              oauthPolling = false
              toasts.show(`OAuth2 service "${svcName}" created`, 'success')
              showServiceModal = false
              loadServices()
            }
          } catch { /* keep polling */ }
        }, 5000)
      } else {
        toasts.show('Error: no auth_url returned', 'error')
      }
    } catch (e: any) {
      toasts.show(e.error || 'OAuth2 flow failed', 'error')
    }
  }

  // --- Secrets ---
  async function loadSecrets() {
    try {
      const data = await api<Secret[]>('/api/vault/secrets')
      secrets = (data || []).sort((a, b) => a.name.localeCompare(b.name))
    } catch { /* silent */ }
  }

  function openSecretModal(secret?: Secret) {
    secretName = secret?.name || ''
    secretValue = ''
    showSecretModal = true
  }

  async function saveSecret() {
    if (!secretName.trim() || !secretValue.trim()) {
      toasts.show('Name and value are required', 'error')
      return
    }
    savingSecret = true
    try {
      await api('/api/vault/secrets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: secretName.trim(), value: secretValue })
      })
      toasts.show('Secret saved', 'success')
      showSecretModal = false
      loadSecrets()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to save', 'error')
    } finally {
      savingSecret = false
    }
  }

  async function deleteSecret(name: string) {
    if (!confirm(`Delete secret "${name}"?`)) return
    try {
      await api(`/api/vault/secrets/${encodeURIComponent(name)}`, { method: 'DELETE' })
      toasts.show('Secret deleted', 'success')
      loadSecrets()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to delete', 'error')
    }
  }

  // --- Files ---
  async function loadFiles() {
    try {
      const data = await api<VaultFile[]>('/api/vault/files')
      files = (data || []).sort((a, b) => a.name.localeCompare(b.name))
    } catch { /* silent */ }
  }

  async function uploadFile() {
    if (!fileInput?.files?.length) return
    uploadingFile = true
    const file = fileInput.files[0]
    const fd = new FormData()
    fd.append('name', file.name)
    fd.append('file', file)
    try {
      await fetch('/api/vault/files', {
        method: 'POST',
        body: fd,
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
      toasts.show('File uploaded', 'success')
      loadFiles()
    } catch (e: any) {
      toasts.show('Upload failed', 'error')
    } finally {
      uploadingFile = false
      if (fileInput) fileInput.value = ''
    }
  }

  function downloadFile(name: string) {
    window.open(`/api/vault/files/${encodeURIComponent(name)}`, '_blank')
  }

  async function deleteFile(name: string) {
    if (!confirm(`Delete file "${name}"?`)) return
    try {
      await api(`/api/vault/files/${encodeURIComponent(name)}`, { method: 'DELETE' })
      toasts.show('File deleted', 'success')
      loadFiles()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to delete', 'error')
    }
  }

  // --- Access tokens ---
  async function loadTokens() {
    try {
      const data = await api<AccessToken[]>('/api/vault/tokens')
      tokens = (data || []).sort((a, b) => (a.scope || '').localeCompare(b.scope || ''))
    } catch { /* silent */ }
  }

  async function createToken() {
    try {
      const data = await api<{ id: string }>('/api/vault/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scope: tokenScope })
      })
      newTokenId = data.id
      showTokenModal = true
      loadTokens()
    } catch (e: any) {
      toasts.show(e.error || 'Failed', 'error')
    }
  }

  async function copyNewToken() {
    try {
      await navigator.clipboard.writeText(newTokenId)
      toasts.show('Token copied to clipboard', 'success')
    } catch {
      toasts.show('Failed to copy', 'error')
    }
  }

  async function revokeToken(id: string) {
    if (!confirm('Revoke this access key?')) return
    try {
      await api(`/api/vault/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })
      toasts.show('Token revoked', 'success')
      loadTokens()
    } catch (e: any) {
      toasts.show(e.error || 'Failed', 'error')
    }
  }

  // --- Export / Import ---
  async function exportVault() {
    if (!exportPassword.trim()) {
      toasts.show('Password required for export', 'error')
      return
    }
    try {
      const res = await fetch('/api/vault/export', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
        body: JSON.stringify({ password: exportPassword }),
        credentials: 'same-origin'
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: 'Export failed' }))
        toasts.show(err.error || 'Export failed', 'error')
        return
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'vault-export.enc'
      a.click()
      URL.revokeObjectURL(url)
      exportPassword = ''
      toasts.show('Vault exported', 'success')
    } catch {
      toasts.show('Export failed', 'error')
    }
  }

  async function importVault() {
    if (!importPassword.trim()) {
      toasts.show('Password required for import', 'error')
      return
    }
    if (!importFileInput?.files?.length) {
      toasts.show('Select an export file', 'error')
      return
    }
    const file = importFileInput.files[0]
    const buf = await file.arrayBuffer()
    const b64 = btoa(String.fromCharCode(...new Uint8Array(buf)))
    try {
      const data = await api<any>('/api/vault/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password: importPassword, data: b64 })
      })
      toasts.show(`Imported ${data.imported || 0} secrets, ${data.services_imported || 0} services`, 'success')
      importPassword = ''
      if (importFileInput) importFileInput.value = ''
      loadAll()
    } catch (e: any) {
      toasts.show(e.error || 'Import failed', 'error')
    }
  }

  function authTypeBadge(type: string): string {
    const map: Record<string, string> = {
      bearer: 'Bearer',
      header: 'Header',
      basic: 'Basic',
      url: 'URL Path',
      oauth2_client: 'OAuth2',
      service_account: 'Service Acct',
      ssh_key: 'SSH'
    }
    return map[type] || type
  }

  onMount(async () => {
    await loadStatus()
    if (vaultStatus === 'unlocked') loadAll()
  })

  onDestroy(() => {
    if (oauthPollTimer) clearInterval(oauthPollTimer)
  })
</script>

<div class="vault-view">
  <div class="vault-title">
    <h2>Vault</h2>
    <span class="status-dot {isUnlocked ? 'dot-unlocked' : 'dot-locked'}" title={isUnlocked ? 'Unlocked' : 'Locked'}></span>
  </div>

  {#if loading}
    <Card><p class="dim">Loading vault status...</p></Card>
  {:else if !available}
    <Card><p class="dim">Vault is not available on this installation.</p></Card>
  {:else if firstTime && vaultStatus !== 'unlocked'}
    <!-- Setup: create vault -->
    <Card>
      <h3><Shield size={16} /> Set Up Vault</h3>
      <p class="form-hint">Create a master password to encrypt your secrets. This cannot be recovered.</p>
      <div class="form-group">
        <label for="setupPw">Master Password</label>
        <input id="setupPw" type="password" class="input" bind:value={password} placeholder="Choose a strong password" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && unlock()} />
      </div>
      <button class="btn btn-primary" onclick={unlock} disabled={unlocking || !password}>
        {unlocking ? 'Creating...' : 'Create Vault'}
      </button>
    </Card>
  {:else if vaultStatus !== 'unlocked'}
    <!-- Unlock -->
    <Card>
      <h3><Lock size={16} /> Unlock Vault</h3>
      <div class="form-group">
        <label for="unlockPw">Master Password</label>
        <input id="unlockPw" type="password" class="input" bind:value={password} placeholder="Enter master password" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && unlock()} />
      </div>
      <button class="btn btn-primary" onclick={unlock} disabled={unlocking || !password}>
        {unlocking ? 'Unlocking...' : 'Unlock'}
      </button>
    </Card>
  {:else}
    <!-- Unlocked content -->

    <!-- Services -->
    <Card>
      <div class="section-header">
        <h3><ExternalLink size={16} /> Services</h3>
        <button class="btn btn-sm" onclick={() => openServiceModal()}>
          <Plus size={13} /> Add
        </button>
      </div>
      {#if services.length === 0}
        <p class="dim">No services configured.</p>
      {:else}
        <div class="item-list">
          {#each services as svc}
            <div class="item-row">
              <div class="item-info">
                <span class="item-name">{svc.name}</span>
                <span class="auth-badge">{authTypeBadge(svc.auth_type)}</span>
                {#if svc.session_cookies}
                  <span class="auth-badge">cookies</span>
                {/if}
                {#if svc.token_status === 'expired'}
                  <span class="token-badge token-expired">expired</span>
                {:else if svc.token_status === 'expiring_soon'}
                  <span class="token-badge token-expiring">expiring</span>
                {/if}
              </div>
              <div class="item-actions">
                <button class="btn btn-sm" onclick={() => openServiceModal(svc)} title="Details">
                  <Info size={12} />
                </button>
                {#if svc.auth_type === 'ssh_key'}
                  <a class="btn btn-sm" href="#/terminal?ssh={encodeURIComponent(svc.name)}" title="Connect SSH">
                    <Terminal size={12} />
                  </a>
                {:else}
                  <button class="btn btn-sm" onclick={() => testService(svc.name)} title="Test connection">
                    <Zap size={12} />
                  </button>
                {/if}
                <button class="btn btn-sm" onclick={() => deleteService(svc.name)} title="Delete">
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </Card>

    <!-- Vault Storage (secrets + files are the same in vault-proxy) -->
    <Card>
      <div class="section-header">
        <h3><Key size={16} /> Secrets & Files</h3>
        <div class="section-header-actions">
          <button class="btn btn-sm" onclick={() => openSecretModal()}>
            <Plus size={13} /> Add Secret
          </button>
          <input type="file" bind:this={fileInput} onchange={uploadFile} style="display:none" />
          <button class="btn btn-sm" onclick={() => fileInput?.click()} disabled={uploadingFile}>
            <Upload size={13} /> {uploadingFile ? 'Uploading...' : 'Upload'}
          </button>
        </div>
      </div>
      {#if secrets.length === 0}
        <p class="dim">No secrets or files stored.</p>
      {:else}
        <div class="item-list">
          {#each secrets as secret}
            <div class="item-row">
              <div class="item-info">
                <span class="item-name">{secret.name}</span>
                <span class="secret-dots">{'•'.repeat(8)}</span>
              </div>
              <div class="item-actions">
                <button class="btn btn-sm" onclick={() => openSecretModal(secret)} title="Edit value">
                  <Pencil size={12} />
                </button>
                <button class="btn btn-sm" onclick={() => downloadFile(secret.name)} title="Download">
                  <Download size={12} />
                </button>
                <button class="btn btn-sm" onclick={() => deleteSecret(secret.name)} title="Delete">
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </Card>

    <!-- Access Keys -->
    <Card>
      <div class="section-header">
        <h3><Shield size={16} /> Access Keys</h3>
        <div class="token-create-row">
          <select class="input input-sm token-scope-select" bind:value={tokenScope}>
            <option value="proxy">proxy</option>
            <option value="admin">admin</option>
          </select>
          <button class="btn btn-sm" onclick={createToken}>
            <Plus size={13} /> Create
          </button>
        </div>
      </div>
      <div class="item-list">
        {#if adminToken}
          <div class="item-row item-row-builtin">
            <div class="item-info">
              <span class="item-name mono">{'•'.repeat(8)}...{adminToken.slice(-4)}</span>
              <span class="auth-badge">admin</span>
            </div>
            <div class="item-actions"><span class="dim" style="font-size:0.7rem">built-in</span></div>
          </div>
        {/if}
        {#if proxyToken}
          <div class="item-row item-row-builtin">
            <div class="item-info">
              <span class="item-name mono">{'•'.repeat(8)}...{proxyToken.slice(-4)}</span>
              <span class="auth-badge">proxy</span>
            </div>
            <div class="item-actions"><span class="dim" style="font-size:0.7rem">built-in</span></div>
          </div>
        {/if}
        {#each tokens as tok}
          <div class="item-row">
            <div class="item-info">
              <span class="item-name mono">{'•'.repeat(8)}...{tok.id.slice(-4)}</span>
              <span class="auth-badge">{tok.scope}</span>
            </div>
            <div class="item-actions">
              <button class="btn btn-sm" onclick={() => revokeToken(tok.id)} title="Revoke">
                <Trash2 size={12} />
              </button>
            </div>
          </div>
        {/each}
        {#if !adminToken && !proxyToken && tokens.length === 0}
          <p class="dim">No access keys.</p>
        {/if}
      </div>
    </Card>

    <!-- Mobile API Token -->
    <Card>
      <div class="section-header">
        <h3><Zap size={16} /> Mobile Access</h3>
      </div>
      <p class="dim" style="font-size:0.8rem;margin-bottom:10px">
        Bearer token for the mobile app.
      </p>
      {#if mobileTokenFull}
        <div class="mobile-token-display">
          <code class="mobile-token-value">{mobileTokenFull}</code>
          <button class="btn btn-sm" onclick={copyMobileToken}>
            {#if mobileTokenCopied}
              <CheckCircle size={12} /> Copied
            {:else}
              <Copy size={12} /> Copy
            {/if}
          </button>
        </div>
        <p class="mobile-token-warning">Copy this token now. It won't be shown again.</p>
        <div class="mobile-token-actions">
          <button class="btn btn-sm" onclick={revokeMobileToken}><Trash2 size={12} /> Revoke</button>
        </div>
      {:else if mobileTokenExists}
        <div class="mobile-token-display">
          <code class="mobile-token-value dim">{mobileTokenMasked}</code>
        </div>
        <div class="mobile-token-actions">
          <button class="btn btn-sm" onclick={generateMobileToken} disabled={mobileGenerating}><RefreshCw size={12} /> Regenerate</button>
          <button class="btn btn-sm" onclick={revokeMobileToken}><Trash2 size={12} /> Revoke</button>
        </div>
      {:else}
        <button class="btn btn-sm btn-primary" onclick={generateMobileToken} disabled={mobileGenerating}>
          <Plus size={12} /> {mobileGenerating ? 'Generating...' : 'Generate Token'}
        </button>
      {/if}
    </Card>

    <!-- Export / Import -->
    <Card>
      <h3>Backup</h3>
      <div class="backup-section">
        <div class="backup-row">
          <h4>Export</h4>
          <div class="backup-form">
            <input type="password" class="input input-sm" bind:value={exportPassword} placeholder="Encryption password" />
            <button class="btn btn-sm" onclick={exportVault} disabled={!exportPassword.trim()}>
              <Download size={13} /> Export
            </button>
          </div>
        </div>
        <div class="backup-row">
          <h4>Import</h4>
          <div class="backup-form">
            <input type="password" class="input input-sm" bind:value={importPassword} placeholder="Decryption password" />
            <input type="file" bind:this={importFileInput} accept=".enc" class="input input-sm" />
            <button class="btn btn-sm" onclick={importVault} disabled={!importPassword.trim()}>
              <Upload size={13} /> Import
            </button>
          </div>
        </div>
      </div>
    </Card>

    <!-- Lock button -->
    <div class="lock-footer">
      <button class="btn" onclick={lockVault}>
        <Lock size={14} /> Lock Vault
      </button>
    </div>
  {/if}
</div>

<!-- Service Modal -->
<Modal open={showServiceModal} onclose={() => showServiceModal = false}>
  <h3>{editingService ? 'Edit' : 'Add'} Service</h3>
  <div class="modal-form">
    <div class="form-group">
      <label>Name</label>
      <input class="input" bind:value={svcName} placeholder="my-api" disabled={!!editingService} />
    </div>
    {#if svcAuthType !== 'ssh_key'}
      <div class="form-group">
        <label>Base URL</label>
        <input class="input" bind:value={svcBaseUrl} placeholder="https://api.example.com" />
      </div>
    {/if}
    <div class="form-group">
      <label>Auth Type</label>
      <select class="input" bind:value={svcAuthType}>
        <option value="bearer">Bearer Token</option>
        <option value="header">Custom Header</option>
        <option value="basic">Basic Auth</option>
        <option value="url">URL Path Token</option>
        <option value="oauth2_client">OAuth2 Client</option>
        <option value="service_account">Service Account (JSON Key)</option>
        <option value="ssh_key">SSH Key</option>
      </select>
    </div>

    {#if svcAuthType === 'bearer'}
      <div class="form-group">
        <div class="ref-toggle">
          <label>Token</label>
          <Toggle bind:checked={svcUseRef} label="Use vault secret" />
        </div>
        {#if svcUseRef}
          <select class="input" bind:value={svcTokenRef}>
            <option value="">Select secret...</option>
            {#each secrets as s}
              <option value={s.name}>{s.name}</option>
            {/each}
          </select>
        {:else}
          <input class="input" type="password" bind:value={svcToken} placeholder="Bearer token" />
        {/if}
      </div>
    {:else if svcAuthType === 'header'}
      <div class="form-group">
        <label>Header Name</label>
        <input class="input" bind:value={svcHeaderName} placeholder="X-API-Key" />
      </div>
      <div class="form-group">
        <div class="ref-toggle">
          <label>Header Value</label>
          <Toggle bind:checked={svcHeaderUseRef} label="Use vault secret" />
        </div>
        {#if svcHeaderUseRef}
          <select class="input" bind:value={svcHeaderValueRef}>
            <option value="">Select secret...</option>
            {#each secrets as s}
              <option value={s.name}>{s.name}</option>
            {/each}
          </select>
        {:else}
          <input class="input" type="password" bind:value={svcHeaderValue} placeholder="Header value" />
        {/if}
      </div>
    {:else if svcAuthType === 'url'}
      <div class="form-group">
        <label>Token <span style="font-weight:normal;color:var(--text-dim)">(replaces <code>{'{token}'}</code> in Base URL)</span></label>
        <input class="input" type="password" bind:value={svcToken} placeholder="API key" />
      </div>
    {:else if svcAuthType === 'basic'}
      <div class="form-group">
        <label>Username</label>
        <input class="input" bind:value={svcUsername} placeholder="Username" />
      </div>
      <div class="form-group">
        <div class="ref-toggle">
          <label>Password</label>
          <Toggle bind:checked={svcBasicUseRef} label="Use vault secret" />
        </div>
        {#if svcBasicUseRef}
          <select class="input" bind:value={svcPasswordRef}>
            <option value="">Select secret...</option>
            {#each secrets as s}
              <option value={s.name}>{s.name}</option>
            {/each}
          </select>
        {:else}
          <input class="input" type="password" bind:value={svcPassword} placeholder="Password" />
        {/if}
      </div>
    {:else if svcAuthType === 'oauth2_client'}
      <div class="oauth-tabs">
        <button class="oauth-tab" class:active={svcOAuthTab === 'browser'} onclick={() => svcOAuthTab = 'browser'}>Browser Flow</button>
        <button class="oauth-tab" class:active={svcOAuthTab === 'manual'} onclick={() => svcOAuthTab = 'manual'}>Manual</button>
      </div>
      {#if svcOAuthTab === 'browser'}
        <div class="form-group">
          <label>Client Secret File</label>
          <select class="input" bind:value={svcOAuthFileRef}>
            <option value="">Select uploaded file...</option>
            {#each files.filter(f => f.name.startsWith('client_secret')) as f}
              <option value={f.name}>{f.name}</option>
            {/each}
          </select>
          <p class="form-hint">Upload a <code>client_secret_*.json</code> file from Google Cloud Console in the Files section below.</p>
        </div>
        <div class="form-group">
          <label>Scopes (comma-separated)</label>
          <input class="input" bind:value={svcOAuthBrowserScopes} placeholder="https://www.googleapis.com/auth/calendar" />
        </div>
        <button class="btn btn-sm" onclick={startOAuth2Flow} disabled={oauthPolling || !svcOAuthFileRef}>
          {#if oauthPolling}
            <Loader2 size={13} class="spin" /> Waiting for authorization...
          {:else}
            <ExternalLink size={13} /> Authorize in Browser
          {/if}
        </button>
      {:else}
        <div class="form-group">
          <label>Client ID</label>
          <input class="input" bind:value={svcClientId} placeholder="Client ID" />
        </div>
        <div class="form-group">
          <label>Client Secret</label>
          <input class="input" type="password" bind:value={svcClientSecret} placeholder="Client Secret" />
        </div>
        <div class="form-group">
          <label>Token URL</label>
          <input class="input" bind:value={svcTokenUrl} placeholder="https://oauth2.googleapis.com/token" />
        </div>
        <div class="form-group">
          <label>Refresh Token</label>
          <input class="input" type="password" bind:value={svcRefreshToken} placeholder="Refresh token (if already obtained)" />
        </div>
        <div class="form-group">
          <label>Scopes (comma-separated)</label>
          <input class="input" bind:value={svcScopes} placeholder="https://www.googleapis.com/auth/calendar" />
        </div>
      {/if}
    {:else if svcAuthType === 'service_account'}
      <div class="form-group">
        <label>JSON Key File</label>
        <select class="input" bind:value={svcSaFileRef}>
          <option value="">Select uploaded file...</option>
          {#each files as f}
            <option value={f.name}>{f.name}</option>
          {/each}
        </select>
        <p class="form-hint">Upload the service account JSON key file in the Files section below.</p>
      </div>
      <div class="form-group">
        <label>Scopes (comma-separated)</label>
        <input class="input" bind:value={svcSaScopes} placeholder="https://www.googleapis.com/auth/spreadsheets" />
      </div>
      <div class="form-group">
        <label>Token URL (optional)</label>
        <input class="input" bind:value={svcSaTokenUrl} placeholder="https://oauth2.googleapis.com/token" />
      </div>
    {:else if svcAuthType === 'ssh_key'}
      <div class="form-group">
        <label>SSH Host</label>
        <input class="input" bind:value={svcSshHost} placeholder="192.168.1.100 or hostname" />
      </div>
      <div class="form-group">
        <label>SSH Port</label>
        <input class="input" type="number" bind:value={svcSshPort} min="1" max="65535" />
      </div>
      <div class="form-group">
        <label>SSH User</label>
        <input class="input" bind:value={svcSshUser} placeholder="root" />
      </div>
      <div class="form-group">
        <label>Private Key File</label>
        <select class="input" bind:value={svcSshKeyFileRef}>
          <option value="">Select uploaded file...</option>
          {#each files as f}
            <option value={f.name}>{f.name}</option>
          {/each}
        </select>
        <p class="form-hint">Upload your SSH private key (PEM format) in the Files section below.</p>
      </div>
      <div class="form-group">
        <label>Key Passphrase (optional)</label>
        <input class="input" type="password" bind:value={svcSshPassphrase} placeholder="Leave empty if key is unprotected" />
      </div>
    {/if}

    {#if svcAuthType !== 'ssh_key'}
      <Toggle bind:checked={svcTlsSkip} label="Skip TLS verification" />
      <Toggle bind:checked={svcSessionCookies} label="Session cookies (sticky sessions)" />
    {/if}

    <div class="modal-actions">
      <button class="btn btn-primary" onclick={saveService} disabled={savingService}>
        {savingService ? 'Saving...' : 'Save'}
      </button>
      <button class="btn" onclick={() => showServiceModal = false}>Cancel</button>
    </div>
  </div>
</Modal>

<!-- Token Created Modal -->
<Modal open={showTokenModal} onclose={() => showTokenModal = false}>
  <h3><Key size={16} /> Token Created</h3>
  <p class="form-hint">Copy this token now. It will not be shown again.</p>
  <div class="token-display">
    <code class="token-value">{newTokenId}</code>
    <button class="btn btn-sm" onclick={copyNewToken} title="Copy">
      <Copy size={13} /> Copy
    </button>
  </div>
  <div class="modal-actions">
    <button class="btn" onclick={() => showTokenModal = false}>Close</button>
  </div>
</Modal>

<!-- Secret Modal -->
<Modal open={showSecretModal} onclose={() => showSecretModal = false}>
  <h3>{secretName ? 'Update' : 'Add'} Secret</h3>
  <div class="modal-form">
    <div class="form-group">
      <label>Name</label>
      <input class="input" bind:value={secretName} placeholder="my-api-key" />
    </div>
    <div class="form-group">
      <label>Value</label>
      <textarea class="input" bind:value={secretValue} placeholder="Secret value..." rows={3}></textarea>
    </div>
    <div class="modal-actions">
      <button class="btn btn-primary" onclick={saveSecret} disabled={savingSecret}>
        {savingSecret ? 'Saving...' : 'Save'}
      </button>
      <button class="btn" onclick={() => showSecretModal = false}>Cancel</button>
    </div>
  </div>
</Modal>

<style>
  .vault-view {
    width: 100%;
    padding: 8px 0;
  }

  .vault-title {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 16px;
  }

  .status-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
  }

  .dot-unlocked { background: var(--green, #3d8b3d); }
  .dot-locked { background: var(--red, #c4392a); }

  .section-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
  }

  .section-header h3 {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
  }

  .item-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .item-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 14px;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    gap: 8px;
    transition: background 0.1s;
  }

  .item-row:hover {
    background: var(--bg-input);
  }

  .item-row-builtin {
    opacity: 0.7;
  }

  .item-info {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
    min-width: 0;
  }

  .item-name {
    font-weight: 500;
    font-size: 0.85rem;
    font-family: 'JetBrains Mono', monospace;
  }

  .item-url {
    font-size: 0.75rem;
    color: var(--text-dim);
    word-break: break-all;
  }

  .item-actions {
    display: flex;
    gap: 4px;
    flex-shrink: 0;
  }

  .auth-badge {
    display: inline-block;
    padding: 1px 6px;
    border-radius: 10px;
    font-size: 0.68rem;
    font-weight: 500;
    background: rgba(59, 130, 246, 0.12);
    color: var(--blue, #3b82f6);
  }

  .vault-storage-tabs {
    display: flex;
    gap: 0;
    margin-bottom: 12px;
    border-bottom: 1px solid var(--border);
  }

  .storage-tab {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 16px;
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    color: var(--text-dim);
    font-family: inherit;
    font-size: 0.82rem;
    font-weight: 500;
    cursor: pointer;
    transition: color 0.15s, border-color 0.15s;
  }

  .storage-tab:hover { color: var(--text); }
  .storage-tab.active { color: var(--accent); border-bottom-color: var(--accent); }

  .tab-count {
    font-size: 0.7rem;
    padding: 0 5px;
    border-radius: 8px;
    background: var(--bg-input);
    color: var(--text-dim);
  }

  .section-header-actions {
    display: flex;
    gap: 4px;
  }

  .secret-dots {
    font-size: 0.7rem;
    color: var(--text-dim);
    letter-spacing: 2px;
  }

  .mono {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.78rem;
  }

  .dim { color: var(--text-dim); font-size: 0.85rem; }

  .form-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: 12px;
  }

  .form-group label {
    font-size: 0.8rem;
    font-weight: 500;
  }

  .form-hint {
    font-size: 0.82rem;
    color: var(--text-dim);
    margin-bottom: 12px;
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

  .input-sm {
    padding: 5px 8px;
    font-size: 0.8rem;
  }

  .ref-toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.82rem;
    cursor: pointer;
  }

  .modal-form {
    margin-top: 12px;
  }

  .form-hint {
    font-size: 0.75rem;
    color: var(--text-dim);
    margin: 4px 0 0;
  }

  .form-hint code {
    background: var(--bg-input);
    padding: 1px 4px;
    border-radius: 3px;
    font-size: 0.7rem;
  }

  .oauth-tabs {
    display: flex;
    gap: 2px;
    margin-bottom: 12px;
    border-bottom: 1px solid var(--border);
    padding-bottom: 8px;
  }

  .oauth-tab {
    padding: 4px 12px;
    background: none;
    border: 1px solid transparent;
    border-radius: 6px;
    color: var(--text-dim);
    font-family: inherit;
    font-size: 0.78rem;
    cursor: pointer;
    transition: all 0.15s;
  }

  .oauth-tab:hover {
    color: var(--text);
    background: var(--bg-input);
  }

  .oauth-tab.active {
    color: var(--text);
    background: var(--bg-card);
    border-color: var(--border);
    font-weight: 500;
  }

  .modal-actions {
    display: flex;
    gap: 8px;
    margin-top: 16px;
  }

  .backup-section {
    display: flex;
    flex-direction: column;
    gap: 16px;
    margin-top: 8px;
  }

  .backup-row h4 {
    margin-bottom: 6px;
    font-size: 0.82rem;
  }

  .backup-form {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .backup-form .input {
    width: auto;
    flex: 1;
    min-width: 150px;
  }

  .lock-footer {
    margin-top: 16px;
    display: flex;
    justify-content: center;
  }

  .file-upload-inline {
    display: flex;
    align-items: center;
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
  .btn-primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
  .btn-primary:hover { opacity: 0.9; }
  .btn-sm { padding: 4px 10px; font-size: 0.75rem; }

  /* Token create row */
  .token-create-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .token-scope-select {
    width: auto;
    min-width: 80px;
    padding: 4px 8px;
    font-size: 0.75rem;
  }

  /* Token status badges */
  .token-badge {
    display: inline-block;
    padding: 1px 6px;
    border-radius: 10px;
    font-size: 0.68rem;
    font-weight: 500;
  }

  .token-expired {
    background: rgba(239, 68, 68, 0.12);
    color: var(--red, #ef4444);
  }

  .token-expiring {
    background: rgba(234, 179, 8, 0.12);
    color: var(--yellow, #eab308);
  }

  .expires-date {
    font-size: 0.68rem;
    color: var(--text-dim);
  }

  /* Token display modal */
  .token-display {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 12px 0;
    padding: 10px 12px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
  }

  .token-value {
    flex: 1;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.78rem;
    word-break: break-all;
    user-select: all;
  }

  /* Mobile token */
  .mobile-token-display {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    margin-bottom: 8px;
  }

  .mobile-token-value {
    flex: 1;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.75rem;
    word-break: break-all;
    user-select: all;
  }

  .mobile-token-warning {
    font-size: 0.75rem;
    color: var(--red);
    font-weight: 500;
    margin-bottom: 8px;
  }

  .mobile-token-actions {
    display: flex;
    gap: 8px;
  }
</style>
