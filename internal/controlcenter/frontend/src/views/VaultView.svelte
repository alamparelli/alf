<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { Lock, Unlock, Shield, Key, FileText, Plus, Trash2, Download, Upload, Eye, EyeOff, Copy, RefreshCw, AlertTriangle, CheckCircle, ExternalLink, Loader2 } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import Modal from '../components/shared/Modal.svelte'
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
  }
  let services = $state<Service[]>([])

  // Secrets
  interface Secret {
    name: string
    set: boolean
  }
  let secrets = $state<Secret[]>([])

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
  let svcAuthUrl = $state('')
  let svcTokenUrl = $state('')
  let svcScopes = $state('')
  let svcRedirectUri = $state('')
  let svcJsonKey = $state('')
  let svcSaScopes = $state('')
  let svcTlsSkip = $state(false)
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

  // Export/Import
  let exportPassword = $state('')
  let importPassword = $state('')
  let importFileInput: HTMLInputElement | undefined

  let isUnlocked = $derived(vaultStatus === 'active')

  async function loadStatus() {
    // Only show loading spinner on first load
    if (!vaultStatus) loading = true
    try {
      const data = await api<any>('/api/vault/status')
      available = data.available
      vaultStatus = data.status
      firstTime = data.first_time
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
      loading = false
      if (vaultStatus === 'active') loadAll()
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
  }

  // --- Services ---
  async function loadServices() {
    try {
      const data = await api<Service[]>('/api/vault/services')
      services = data || []
    } catch { /* silent */ }
  }

  function openServiceModal(svc?: Service) {
    editingService = svc || null
    svcName = svc?.name || ''
    svcBaseUrl = svc?.base_url || ''
    svcAuthType = svc?.auth_type || 'bearer'
    svcTlsSkip = svc?.tls_skip_verify || false
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
    svcAuthUrl = ''
    svcTokenUrl = ''
    svcScopes = ''
    svcRedirectUri = window.location.origin + '/api/vault/oauth2/callback'
    svcJsonKey = ''
    svcSaScopes = ''
    showServiceModal = true
  }

  async function saveService() {
    if (!svcName.trim() || !svcBaseUrl.trim()) {
      toasts.show('Name and base URL are required', 'error')
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
        case 'basic':
          auth.username = svcUsername
          if (svcBasicUseRef && svcPasswordRef) auth.password_ref = svcPasswordRef
          else auth.password = svcPassword
          break
        case 'oauth2_client':
          auth.client_id = svcClientId
          auth.client_secret = svcClientSecret
          auth.auth_url = svcAuthUrl
          auth.token_url = svcTokenUrl
          auth.scopes = svcScopes
          auth.redirect_uri = svcRedirectUri
          break
        case 'service_account':
          auth.json_key = svcJsonKey
          auth.scopes = svcSaScopes
          break
      }
      await api('/api/vault/services', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: svcName.trim(),
          base_url: svcBaseUrl.trim(),
          auth,
          tls_skip_verify: svcTlsSkip
        })
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
      if (data.ok) toasts.show(`${name}: connection OK`, 'success')
      else toasts.show(`${name}: ${data.error || 'test failed'}`, 'error')
    } catch (e: any) {
      toasts.show(e.error || 'Test failed', 'error')
    }
  }

  // OAuth2 flow
  async function startOAuth2Flow() {
    try {
      const data = await api<any>('/api/vault/oauth2/authorize', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: svcName.trim(),
          base_url: svcBaseUrl.trim(),
          client_id: svcClientId,
          client_secret: svcClientSecret,
          auth_url: svcAuthUrl,
          token_url: svcTokenUrl,
          scopes: svcScopes,
          redirect_uri: svcRedirectUri
        })
      })
      if (data.authorize_url) {
        window.open(data.authorize_url, '_blank')
        oauthPolling = true
        // Poll for completion
        oauthPollTimer = setInterval(async () => {
          try {
            const svcList = await api<Service[]>('/api/vault/services')
            const found = svcList.find(s => s.name === svcName.trim())
            if (found) {
              clearInterval(oauthPollTimer)
              oauthPolling = false
              toasts.show('OAuth2 flow completed', 'success')
              showServiceModal = false
              loadServices()
            }
          } catch { /* keep polling */ }
        }, 3000)
      }
    } catch (e: any) {
      toasts.show(e.error || 'OAuth2 flow failed', 'error')
    }
  }

  // --- Secrets ---
  async function loadSecrets() {
    try {
      const data = await api<Secret[]>('/api/vault/secrets')
      secrets = data || []
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
      files = data || []
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
      tokens = data || []
    } catch { /* silent */ }
  }

  async function createToken() {
    try {
      const data = await api<{ id: string }>('/api/vault/tokens', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scope: 'proxy' })
      })
      toasts.show('Token created: ' + data.id, 'success')
      loadTokens()
    } catch (e: any) {
      toasts.show(e.error || 'Failed', 'error')
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
      oauth2_client: 'OAuth2',
      service_account: 'Service Acct'
    }
    return map[type] || type
  }

  onMount(() => {
    loadStatus()
  })

  $effect(() => {
    if (isUnlocked) loadAll()
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
  {:else if firstTime && vaultStatus !== 'active'}
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
  {:else if vaultStatus !== 'active'}
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
                <span class="item-url">{svc.base_url}</span>
                <span class="auth-badge">{authTypeBadge(svc.auth_type)}</span>
              </div>
              <div class="item-actions">
                <button class="btn btn-sm" onclick={() => testService(svc.name)} title="Test">
                  <CheckCircle size={12} />
                </button>
                <button class="btn btn-sm" onclick={() => openServiceModal(svc)} title="Edit">
                  <RefreshCw size={12} />
                </button>
                <button class="btn btn-sm" onclick={() => deleteService(svc.name)} title="Delete">
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </Card>

    <!-- Secrets -->
    <Card>
      <div class="section-header">
        <h3><Key size={16} /> Secrets</h3>
        <button class="btn btn-sm" onclick={() => openSecretModal()}>
          <Plus size={13} /> Add
        </button>
      </div>
      {#if secrets.length === 0}
        <p class="dim">No secrets stored.</p>
      {:else}
        <div class="item-list">
          {#each secrets as secret}
            <div class="item-row">
              <div class="item-info">
                <span class="item-name">{secret.name}</span>
                <span class="dim" style="font-size:0.75rem">***</span>
              </div>
              <div class="item-actions">
                <button class="btn btn-sm" onclick={() => openSecretModal(secret)} title="Update">
                  <RefreshCw size={12} />
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

    <!-- Files -->
    <Card>
      <div class="section-header">
        <h3><FileText size={16} /> Encrypted Files</h3>
        <div class="file-upload-inline">
          <input type="file" bind:this={fileInput} onchange={uploadFile} style="display:none" />
          <button class="btn btn-sm" onclick={() => fileInput?.click()} disabled={uploadingFile}>
            <Upload size={13} /> {uploadingFile ? 'Uploading...' : 'Upload'}
          </button>
        </div>
      </div>
      {#if files.length === 0}
        <p class="dim">No encrypted files.</p>
      {:else}
        <div class="item-list">
          {#each files as f}
            <div class="item-row">
              <div class="item-info">
                <span class="item-name">{f.name}</span>
              </div>
              <div class="item-actions">
                <button class="btn btn-sm" onclick={() => downloadFile(f.name)} title="Download">
                  <Download size={12} />
                </button>
                <button class="btn btn-sm" onclick={() => deleteFile(f.name)} title="Delete">
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
        <button class="btn btn-sm" onclick={createToken}>
          <Plus size={13} /> Create
        </button>
      </div>
      {#if tokens.length === 0}
        <p class="dim">No access keys.</p>
      {:else}
        <div class="item-list">
          {#each tokens as tok}
            <div class="item-row">
              <div class="item-info">
                <span class="item-name mono">{tok.id.length > 16 ? tok.id.slice(0, 16) + '...' : tok.id}</span>
                <span class="auth-badge">{tok.scope}</span>
              </div>
              <div class="item-actions">
                <button class="btn btn-sm" onclick={() => revokeToken(tok.id)} title="Revoke">
                  <Trash2 size={12} />
                </button>
              </div>
            </div>
          {/each}
        </div>
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
    <div class="form-group">
      <label>Base URL</label>
      <input class="input" bind:value={svcBaseUrl} placeholder="https://api.example.com" />
    </div>
    <div class="form-group">
      <label>Auth Type</label>
      <select class="input" bind:value={svcAuthType}>
        <option value="bearer">Bearer Token</option>
        <option value="header">Custom Header</option>
        <option value="basic">Basic Auth</option>
        <option value="oauth2_client">OAuth2 Client</option>
        <option value="service_account">Service Account (JSON Key)</option>
      </select>
    </div>

    {#if svcAuthType === 'bearer'}
      <div class="form-group">
        <div class="ref-toggle">
          <label>Token</label>
          <label class="checkbox-label">
            <input type="checkbox" bind:checked={svcUseRef} /> Use vault secret
          </label>
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
          <label class="checkbox-label">
            <input type="checkbox" bind:checked={svcHeaderUseRef} /> Use vault secret
          </label>
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
    {:else if svcAuthType === 'basic'}
      <div class="form-group">
        <label>Username</label>
        <input class="input" bind:value={svcUsername} placeholder="Username" />
      </div>
      <div class="form-group">
        <div class="ref-toggle">
          <label>Password</label>
          <label class="checkbox-label">
            <input type="checkbox" bind:checked={svcBasicUseRef} /> Use vault secret
          </label>
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
      <div class="form-group">
        <label>Client ID</label>
        <input class="input" bind:value={svcClientId} placeholder="Client ID" />
      </div>
      <div class="form-group">
        <label>Client Secret</label>
        <input class="input" type="password" bind:value={svcClientSecret} placeholder="Client Secret" />
      </div>
      <div class="form-group">
        <label>Auth URL</label>
        <input class="input" bind:value={svcAuthUrl} placeholder="https://accounts.google.com/o/oauth2/v2/auth" />
      </div>
      <div class="form-group">
        <label>Token URL</label>
        <input class="input" bind:value={svcTokenUrl} placeholder="https://oauth2.googleapis.com/token" />
      </div>
      <div class="form-group">
        <label>Scopes (space-separated)</label>
        <input class="input" bind:value={svcScopes} placeholder="https://www.googleapis.com/auth/calendar" />
      </div>
      <div class="form-group">
        <label>Redirect URI</label>
        <input class="input" bind:value={svcRedirectUri} />
      </div>
      <button class="btn btn-sm" onclick={startOAuth2Flow} disabled={oauthPolling}>
        {#if oauthPolling}
          <Loader2 size={13} class="spin" /> Waiting for authorization...
        {:else}
          <ExternalLink size={13} /> Start OAuth2 Flow
        {/if}
      </button>
    {:else if svcAuthType === 'service_account'}
      <div class="form-group">
        <label>JSON Key</label>
        <textarea class="input" bind:value={svcJsonKey} placeholder='Paste JSON key file contents...' rows={5}></textarea>
      </div>
      <div class="form-group">
        <label>Scopes (space-separated)</label>
        <input class="input" bind:value={svcSaScopes} placeholder="https://www.googleapis.com/auth/spreadsheets" />
      </div>
    {/if}

    <label class="checkbox-label">
      <input type="checkbox" bind:checked={svcTlsSkip} /> Skip TLS verification
    </label>

    <div class="modal-actions">
      <button class="btn btn-primary" onclick={saveService} disabled={savingService}>
        {savingService ? 'Saving...' : 'Save'}
      </button>
      <button class="btn" onclick={() => showServiceModal = false}>Cancel</button>
    </div>
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
    padding: 8px 10px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
    gap: 8px;
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
</style>
