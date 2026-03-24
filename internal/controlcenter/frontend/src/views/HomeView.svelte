<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import {
    Activity, Folder, FolderOpen, File, FileText, FilePlus, FolderPlus,
    Trash2, Download, Upload, Save, Eye, Pencil, ChevronRight, ChevronDown,
    X, BookOpen, Brain, Zap, Package, Loader2, RefreshCw, Clock, MessageCircle, Calendar, Layers
  } from 'lucide-svelte'
  import Card from '../components/shared/Card.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'

  // --- Activity ---
  interface ActivityItem {
    type: string
    name: string
    started_at?: string
    elapsed?: string
  }
  let activityItems = $state<ActivityItem[]>([])
  let activityTimer: ReturnType<typeof setInterval> | undefined

  function activityIcon(type: string) {
    switch (type) {
      case 'chat': return MessageCircle
      case 'schedule': return Calendar
      case 'task': return Layers
      default: return Activity
    }
  }

  async function loadActivity() {
    try {
      const data = await api<{ items: ActivityItem[]; count: number }>('/api/activity')
      activityItems = data.items || []
    } catch { /* silent */ }
  }

  // --- Workspace ---
  interface WsEntry {
    name: string
    is_dir: boolean
    size: number
  }
  interface WsDir {
    type: 'directory'
    path: string
    entries: WsEntry[]
    protected?: string[]
    readOnly?: string[]
  }
  interface WsFile {
    type: 'file'
    name: string
    size: number
    editable: boolean
    content?: string
    message?: string
  }

  let currentPath = $state('')
  let dirEntries = $state<WsEntry[]>([])
  let protectedDirs = $state<string[]>([])
  let expandedDirs = $state<Record<string, WsEntry[]>>({})
  let loadingDir = $state(false)

  // File viewer
  let viewingFile = $state<WsFile | null>(null)
  let viewingPath = $state('')
  let fileEditMode = $state(false)
  let fileEditContent = $state('')
  let savingFile = $state(false)
  let mdPreview = $state(true)

  // JSON viewer
  let jsonExpanded = $state<Record<string, boolean>>({})

  // Create file/dir
  let showCreateModal = $state(false)
  let createType = $state<'file' | 'dir'>('file')
  let createName = $state('')
  let createPath = $state('')

  // Delete confirm
  let deleteTarget = $state('')

  // Upload
  let showUploadModal = $state(false)
  let uploadFiles = $state<FileList | null>(null)
  let uploading = $state(false)
  let dragOver = $state(false)

  // Teach
  let teachContent = $state('')
  let teachInstruction = $state('extract key facts')
  let teachDestination = $state('memory')
  let teachFileName = $state('')
  let teachTier = $state('')
  let teachTiers = $state<{ name: string; model: string; tools: boolean }[]>([])
  let teaching = $state(false)
  let teachResult = $state<any>(null)
  let teachContentLen = $derived(teachContent.length)

  // Skill store
  let skillCommand = $state('')
  let scanning = $state(false)
  let scanResult = $state<any>(null)
  let installing = $state(false)
  let skillOverwrite = $state(false)

  // Path breadcrumbs
  let breadcrumbs = $derived(() => {
    if (!currentPath) return []
    const parts = currentPath.split('/')
    const crumbs: { name: string; path: string }[] = []
    for (let i = 0; i < parts.length; i++) {
      crumbs.push({ name: parts[i], path: parts.slice(0, i + 1).join('/') })
    }
    return crumbs
  })

  function fileExt(name: string): string {
    const idx = name.lastIndexOf('.')
    return idx >= 0 ? name.slice(idx + 1).toLowerCase() : ''
  }

  function isMarkdown(name: string): boolean {
    return ['md', 'markdown'].includes(fileExt(name))
  }

  function isJson(name: string): boolean {
    return fileExt(name) === 'json'
  }

  function isJsonl(name: string): boolean {
    return fileExt(name) === 'jsonl'
  }

  function isCsv(name: string): boolean {
    return fileExt(name) === 'csv'
  }

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  }

  function renderMd(text: string): string {
    return DOMPurify.sanitize(marked.parse(text) as string)
  }

  async function loadDir(path: string) {
    loadingDir = true
    try {
      const data = await api<WsDir>(`/api/workspace?path=${encodeURIComponent(path)}`)
      if (data.type === 'directory') {
        dirEntries = data.entries || []
        currentPath = path
        if (data.protected) protectedDirs = data.protected
        viewingFile = null
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load directory', 'error')
    } finally {
      loadingDir = false
    }
  }

  async function loadSubDir(parentPath: string, dirName: string) {
    const fullPath = parentPath ? `${parentPath}/${dirName}` : dirName
    const key = fullPath
    if (expandedDirs[key]) {
      delete expandedDirs[key]
      expandedDirs = { ...expandedDirs }
      return
    }
    try {
      const data = await api<WsDir>(`/api/workspace?path=${encodeURIComponent(fullPath)}`)
      if (data.type === 'directory') {
        expandedDirs[key] = data.entries || []
        expandedDirs = { ...expandedDirs }
      }
    } catch { /* silent */ }
  }

  async function openFile(path: string) {
    try {
      const data = await api<WsFile>(`/api/workspace?path=${encodeURIComponent(path)}`)
      if (data.type === 'file') {
        viewingFile = data
        viewingPath = path
        fileEditMode = false
        fileEditContent = data.content || ''
        mdPreview = isMarkdown(data.name)
        jsonExpanded = {}
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to open file', 'error')
    }
  }

  async function saveFile() {
    if (!viewingPath) return
    savingFile = true
    try {
      await api(`/api/workspace?path=${encodeURIComponent(viewingPath)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/octet-stream' },
        body: fileEditContent
      })
      toasts.show('File saved', 'success')
      viewingFile = { ...viewingFile!, content: fileEditContent }
      fileEditMode = false
    } catch (e: any) {
      toasts.show(e.error || 'Failed to save', 'error')
    } finally {
      savingFile = false
    }
  }

  async function deleteEntry(path: string) {
    if (!confirm(`Delete "${path}"?`)) return
    try {
      await api(`/api/workspace?path=${encodeURIComponent(path)}`, { method: 'DELETE' })
      toasts.show('Deleted', 'success')
      if (viewingPath === path) viewingFile = null
      loadDir(currentPath)
    } catch (e: any) {
      toasts.show(e.error || 'Failed to delete', 'error')
    }
  }

  function isProtected(name: string): boolean {
    return protectedDirs.includes(name)
  }

  async function createEntry() {
    if (!createName.trim()) return
    const path = createPath ? `${createPath}/${createName.trim()}` : createName.trim()
    try {
      if (createType === 'dir') {
        // Create dir by creating a placeholder file and deleting it? No, PUT with empty body creates parent dirs.
        // Actually the workspace handler expects PUT for files. For dirs, we create a .keep file.
        await api(`/api/workspace?path=${encodeURIComponent(path + '/.keep')}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/octet-stream' },
          body: ''
        })
      } else {
        await api(`/api/workspace?path=${encodeURIComponent(path)}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/octet-stream' },
          body: ''
        })
      }
      toasts.show(`${createType === 'dir' ? 'Directory' : 'File'} created`, 'success')
      showCreateModal = false
      createName = ''
      loadDir(currentPath)
    } catch (e: any) {
      toasts.show(e.error || 'Failed to create', 'error')
    }
  }

  function openCreateDialog(type: 'file' | 'dir') {
    createType = type
    createPath = currentPath
    createName = ''
    showCreateModal = true
  }

  // Upload
  async function handleUpload() {
    if (!uploadFiles?.length) return
    uploading = true
    const fd = new FormData()
    fd.append('path', currentPath)
    for (let i = 0; i < uploadFiles.length; i++) {
      fd.append('files', uploadFiles[i])
    }
    try {
      await fetch('/api/workspace/upload', {
        method: 'POST',
        body: fd,
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' }
      })
      toasts.show(`${uploadFiles.length} file(s) uploaded`, 'success')
      showUploadModal = false
      uploadFiles = null
      loadDir(currentPath)
    } catch {
      toasts.show('Upload failed', 'error')
    } finally {
      uploading = false
    }
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault()
    dragOver = true
  }

  function handleDragLeave() {
    dragOver = false
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault()
    dragOver = false
    if (e.dataTransfer?.files?.length) {
      uploadFiles = e.dataTransfer.files
      showUploadModal = true
    }
  }

  // --- JSON viewer ---
  function parseJson(content: string): any {
    try { return JSON.parse(content) } catch { return null }
  }

  function toggleJsonKey(path: string) {
    jsonExpanded[path] = !jsonExpanded[path]
    jsonExpanded = { ...jsonExpanded }
  }

  function jsonTypeLabel(val: any): string {
    if (val === null) return 'null'
    if (Array.isArray(val)) return `array[${val.length}]`
    if (typeof val === 'object') return `object{${Object.keys(val).length}}`
    return typeof val
  }

  // --- JSONL ---
  function parseJsonl(content: string): any[] {
    return content.split('\n').filter(l => l.trim()).map(l => {
      try { return JSON.parse(l) } catch { return { _raw: l } }
    })
  }

  // --- CSV ---
  function parseCsv(content: string): { headers: string[]; rows: string[][] } {
    const lines = content.split('\n').filter(l => l.trim())
    if (lines.length === 0) return { headers: [], rows: [] }
    const headers = lines[0].split(',').map(h => h.trim())
    const rows = lines.slice(1).map(l => l.split(',').map(c => c.trim()))
    return { headers, rows }
  }

  // --- Teach ---
  async function loadTiers() {
    try {
      const data = await api<any[]>('/api/memory/tiers')
      teachTiers = data || []
    } catch { /* silent */ }
  }

  async function teach() {
    if (!teachContent.trim()) return
    if (teachDestination === 'context' && !teachFileName.trim()) {
      toasts.show('File name is required for context destination', 'error')
      return
    }
    teaching = true
    teachResult = null
    try {
      const body: any = {
        content: teachContent,
        instruction: teachInstruction,
        destination: teachDestination
      }
      if (teachTier) body.tier = teachTier
      if (teachDestination === 'context') body.file_name = teachFileName.trim()

      const data = await api<any>('/api/memory/ingest', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      })
      teachResult = data
      toasts.show(`Imported ${data.imported || 0} items${data.skipped ? `, ${data.skipped} skipped` : ''}`, 'success')
      teachContent = ''
    } catch (e: any) {
      toasts.show(e.error || 'Ingest failed', 'error')
    } finally {
      teaching = false
    }
  }

  // --- Skill store ---
  async function scanSkill() {
    if (!skillCommand.trim()) return
    scanning = true
    scanResult = null
    try {
      const data = await api<any>('/api/skills/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action: 'scan', command: skillCommand.trim() })
      })
      scanResult = data
    } catch (e: any) {
      if (e.available_skills) {
        scanResult = { error: e.error, available_skills: e.available_skills, hint: e.hint }
      } else {
        toasts.show(e.error || 'Scan failed', 'error')
      }
    } finally {
      scanning = false
    }
  }

  async function installSkill() {
    if (!scanResult) return
    installing = true
    try {
      const data = await api<any>('/api/skills/import', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: 'install',
          name: scanResult.name,
          content: scanResult.content,
          triggers: (scanResult.triggers || []).join(', '),
          tier: scanResult.tier || '',
          source: scanResult.source || '',
          overwrite: skillOverwrite
        })
      })
      toasts.show(`Skill "${data.name}" installed at ${data.path}`, 'success')
      scanResult = null
      skillCommand = ''
    } catch (e: any) {
      toasts.show(e.error || 'Install failed', 'error')
    } finally {
      installing = false
    }
  }

  function handleOpenFile(e: CustomEvent<{ path: string }>) {
    openFile(e.detail.path)
  }

  onMount(() => {
    loadDir('')
    loadActivity()
    loadTiers()
    activityTimer = setInterval(loadActivity, 5000)
    window.addEventListener('alf:open-file', handleOpenFile as EventListener)
  })

  onDestroy(() => {
    if (activityTimer) clearInterval(activityTimer)
    window.removeEventListener('alf:open-file', handleOpenFile as EventListener)
  })
</script>

<div class="home-view" ondragover={handleDragOver} ondragleave={handleDragLeave} ondrop={handleDrop} role="main">
  {#if dragOver}
    <div class="drag-overlay">
      <Upload size={48} />
      <p>Drop files to upload</p>
    </div>
  {/if}

  <h2>Home</h2>

  <!-- Activity bar -->
  {#if activityItems.length > 0}
    <Card>
      <div class="activity-bar">
        {#each activityItems as item}
          <div class="activity-item">
            <svelte:component this={activityIcon(item.type)} size={14} />
            <span class="activity-name">{item.name}</span>
            {#if item.elapsed}
              <span class="activity-elapsed"><Clock size={11} /> {item.elapsed}</span>
            {/if}
            <span class="activity-badge">{item.type}</span>
          </div>
        {/each}
      </div>
    </Card>
  {/if}

  <!-- Workspace (two-column) -->
  <div class="workspace-layout">
  <Card>
    <div class="section-header">
      <h3><Folder size={16} /> Workspace</h3>
      <div class="ws-actions">
        <button class="btn btn-sm" onclick={() => openCreateDialog('file')} title="New file">
          <FilePlus size={13} />
        </button>
        <button class="btn btn-sm" onclick={() => openCreateDialog('dir')} title="New directory">
          <FolderPlus size={13} />
        </button>
        <button class="btn btn-sm" onclick={() => { showUploadModal = true }} title="Upload">
          <Upload size={13} />
        </button>
        <button class="btn btn-sm" onclick={() => loadDir(currentPath)} title="Refresh">
          <RefreshCw size={13} />
        </button>
      </div>
    </div>

    <!-- Breadcrumbs -->
    <div class="breadcrumbs">
      <button class="breadcrumb" onclick={() => loadDir('')}>data</button>
    </div>

    <!-- Directory listing (tree view) -->
    {#if loadingDir}
      <p class="dim">Loading...</p>
    {:else}
      <div class="file-tree">
        {#snippet treeNode(entries: WsEntry[], parentPath: string, depth: number)}
          {#each entries as entry}
            {#if entry.is_dir}
              {@const fullPath = parentPath ? `${parentPath}/${entry.name}` : entry.name}
              {@const isExpanded = !!expandedDirs[fullPath]}
              <div class="tree-row dir-row" style="padding-left:{depth * 16 + 8}px">
                <div class="file-row-main" onclick={() => loadSubDir(parentPath, entry.name)} role="button" tabindex="0" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && loadSubDir(parentPath, entry.name)}>
                  {#if isExpanded}
                    <ChevronDown size={14} />
                    <FolderOpen size={14} />
                  {:else}
                    <ChevronRight size={14} />
                    <Folder size={14} />
                  {/if}
                  <span class="file-name">{entry.name}</span>
                  {#if isProtected(entry.name) && depth === 0}
                    <span class="protected-badge">protected</span>
                  {/if}
                </div>
                {#if !(isProtected(entry.name) && depth === 0)}
                  <button class="btn-icon" onclick={() => deleteEntry(fullPath)} title="Delete">
                    <Trash2 size={12} />
                  </button>
                {/if}
              </div>
              {#if isExpanded && expandedDirs[fullPath]}
                {@render treeNode(expandedDirs[fullPath], fullPath, depth + 1)}
              {/if}
            {:else}
              {@const fullPath = parentPath ? `${parentPath}/${entry.name}` : entry.name}
              <div class="tree-row" style="padding-left:{depth * 16 + 8}px">
                <div class="file-row-main" onclick={() => openFile(fullPath)} role="button" tabindex="0" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && openFile(fullPath)}>
                  <FileText size={14} />
                  <span class="file-name">{entry.name}</span>
                  <span class="file-size">{formatSize(entry.size)}</span>
                </div>
                <button class="btn-icon" onclick={() => deleteEntry(fullPath)} title="Delete">
                  <Trash2 size={12} />
                </button>
              </div>
            {/if}
          {/each}
        {/snippet}
        {@render treeNode(dirEntries, currentPath, 0)}
        {#if dirEntries.length === 0}
          <p class="dim">Empty workspace.</p>
        {/if}
      </div>
    {/if}
  </Card>

  <!-- File viewer -->
  {#if viewingFile}
    <Card>
      <div class="section-header">
        <h3><FileText size={16} /> {viewingFile.name}</h3>
        <div class="ws-actions">
          {#if viewingFile.editable && viewingFile.content !== undefined}
            {#if fileEditMode}
              <button class="btn btn-sm btn-primary" onclick={saveFile} disabled={savingFile}>
                <Save size={13} /> {savingFile ? 'Saving...' : 'Save'}
              </button>
              <button class="btn btn-sm" onclick={() => { fileEditMode = false; fileEditContent = viewingFile!.content || '' }}>Cancel</button>
            {:else}
              <button class="btn btn-sm" onclick={() => fileEditMode = true}>
                <Pencil size={13} /> Edit
              </button>
            {/if}
          {/if}
          {#if isMarkdown(viewingFile.name) && !fileEditMode}
            <button class="btn btn-sm" onclick={() => mdPreview = !mdPreview}>
              {#if mdPreview}
                <Pencil size={13} /> Source
              {:else}
                <Eye size={13} /> Preview
              {/if}
            </button>
          {/if}
          <button class="btn btn-sm" onclick={() => viewingFile = null}>
            <X size={13} />
          </button>
        </div>
      </div>

      <div class="file-meta">
        <span>{formatSize(viewingFile.size)}</span>
        {#if !viewingFile.editable}
          <span class="dim">read-only</span>
        {/if}
      </div>

      {#if viewingFile.message}
        <p class="dim">{viewingFile.message}</p>
        {#if viewingFile.message.includes('Binary')}
          <a href={`/api/workspace?path=${encodeURIComponent(viewingPath)}`} download class="btn btn-sm" style="margin-top:8px">
            <Download size={13} /> Download
          </a>
        {/if}
      {:else if viewingFile.content !== undefined}
        {#if fileEditMode}
          <textarea class="input file-editor" bind:value={fileEditContent} rows={20}></textarea>
        {:else if isMarkdown(viewingFile.name) && mdPreview}
          <div class="markdown-body">{@html renderMd(viewingFile.content)}</div>
        {:else if isJson(viewingFile.name)}
          {@const parsed = parseJson(viewingFile.content)}
          {#if parsed !== null}
            <div class="json-viewer">
              {#snippet jsonNode(val: any, path: string, depth: number)}
                {#if val === null}
                  <span class="json-null">null</span>
                {:else if typeof val === 'boolean'}
                  <span class="json-bool">{val.toString()}</span>
                {:else if typeof val === 'number'}
                  <span class="json-num">{val}</span>
                {:else if typeof val === 'string'}
                  <span class="json-str">"{val.length > 200 ? val.slice(0, 200) + '...' : val}"</span>
                {:else if Array.isArray(val)}
                  <span class="json-toggle" onclick={() => toggleJsonKey(path)} role="button" tabindex="0" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && toggleJsonKey(path)}>
                    {jsonExpanded[path] ? '[-]' : '[+]'} <span class="json-type">array[{val.length}]</span>
                  </span>
                  {#if jsonExpanded[path]}
                    <div class="json-children" style="margin-left:{Math.min(depth, 4) * 16}px">
                      {#each val as item, i}
                        <div class="json-entry">
                          <span class="json-key">{i}:</span>
                          {@render jsonNode(item, `${path}.${i}`, depth + 1)}
                        </div>
                      {/each}
                    </div>
                  {/if}
                {:else if typeof val === 'object'}
                  <span class="json-toggle" onclick={() => toggleJsonKey(path)} role="button" tabindex="0" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && toggleJsonKey(path)}>
                    {jsonExpanded[path] ? '{-}' : '{+}'} <span class="json-type">object&lbrace;{Object.keys(val).length}&rbrace;</span>
                  </span>
                  {#if jsonExpanded[path]}
                    <div class="json-children" style="margin-left:{Math.min(depth, 4) * 16}px">
                      {#each Object.entries(val) as [k, v]}
                        <div class="json-entry">
                          <span class="json-key">"{k}":</span>
                          {@render jsonNode(v, `${path}.${k}`, depth + 1)}
                        </div>
                      {/each}
                    </div>
                  {/if}
                {/if}
              {/snippet}
              {@render jsonNode(parsed, 'root', 0)}
            </div>
          {:else}
            <pre class="file-content">{viewingFile.content}</pre>
          {/if}
        {:else if isJsonl(viewingFile.name)}
          {@const lines = parseJsonl(viewingFile.content)}
          <div class="jsonl-viewer">
            {#each lines as line, i}
              <div class="jsonl-line">
                <span class="jsonl-num">{i + 1}</span>
                <pre class="jsonl-content">{JSON.stringify(line, null, 2)}</pre>
              </div>
            {/each}
          </div>
        {:else if isCsv(viewingFile.name)}
          {@const { headers, rows } = parseCsv(viewingFile.content)}
          <div class="csv-table-wrap">
            <table class="csv-table">
              <thead>
                <tr>
                  {#each headers as h}
                    <th>{h}</th>
                  {/each}
                </tr>
              </thead>
              <tbody>
                {#each rows as row}
                  <tr>
                    {#each row as cell}
                      <td>{cell}</td>
                    {/each}
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}
          <pre class="file-content">{viewingFile.content}</pre>
        {/if}
      {/if}
    </Card>
  {:else}
    <div class="file-viewer-placeholder">
      <Eye size={24} />
      <p>Select a file to view</p>
    </div>
  {/if}
  </div>

  <!-- Teach -->
  <Card>
    <h3><Brain size={16} /> Teach</h3>
    <p class="form-hint">Feed knowledge into ALF's memory or context files.</p>

    <div class="teach-toggles">
      <label class="radio-label">
        <input type="radio" value="memory" bind:group={teachDestination} /> Memory
      </label>
      <label class="radio-label">
        <input type="radio" value="context" bind:group={teachDestination} /> Context file
      </label>
    </div>

    {#if teachDestination === 'context'}
      <div class="form-group">
        <label>File name (without extension)</label>
        <input class="input" bind:value={teachFileName} placeholder="my-knowledge" />
      </div>
    {/if}

    <div class="form-group">
      <label>Instruction</label>
      <select class="input" bind:value={teachInstruction}>
        <option value="extract key facts">Extract key facts</option>
        <option value="extract preferences">Extract preferences</option>
        <option value="extract decisions">Extract decisions</option>
        <option value="store-as-is">Store as-is</option>
        <option value="summarize">Summarize</option>
      </select>
    </div>

    {#if teachTiers.length > 0}
      <div class="form-group">
        <label>Tier (optional)</label>
        <select class="input" bind:value={teachTier}>
          <option value="">Auto</option>
          {#each teachTiers as tier}
            <option value={tier.name}>{tier.name} ({tier.model})</option>
          {/each}
        </select>
      </div>
    {/if}

    <div class="form-group">
      <label>Content <span class="content-counter">{teachContentLen}/51200</span></label>
      <textarea class="input" bind:value={teachContent} rows={6} placeholder="Paste text, notes, documentation..."></textarea>
    </div>

    <button class="btn btn-primary" onclick={teach} disabled={teaching || !teachContent.trim()}>
      {#if teaching}
        <Loader2 size={14} class="spin" /> Ingesting...
      {:else}
        <Zap size={14} /> Ingest
      {/if}
    </button>

    {#if teachResult}
      <div class="teach-result">
        {#if teachResult.imported !== undefined}
          <p>Imported: {teachResult.imported}{teachResult.skipped ? `, Skipped: ${teachResult.skipped}` : ''}</p>
        {/if}
        {#if teachResult.file_name}
          <p>Saved to: {teachResult.file_name}</p>
        {/if}
        {#if teachResult.memories}
          <div class="teach-memories">
            {#each teachResult.memories as mem}
              <div class="teach-mem">
                <span class="auth-badge">{mem.type}</span>
                <span>{mem.text}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
  </Card>

  <!-- Skill store -->
  <Card>
    <h3><Package size={16} /> Skill Store</h3>
    <p class="form-hint">Import skills from GitHub repositories.</p>

    <div class="form-group">
      <label>Repository or URL</label>
      <input class="input" bind:value={skillCommand} placeholder="owner/repo or https://github.com/owner/repo" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && scanSkill()} />
    </div>

    <button class="btn btn-primary" onclick={scanSkill} disabled={scanning || !skillCommand.trim()}>
      {#if scanning}
        <Loader2 size={14} class="spin" /> Scanning...
      {:else}
        <Zap size={14} /> Scan
      {/if}
    </button>

    {#if scanResult}
      {#if scanResult.available_skills}
        <div class="scan-pick">
          <p>Available skills in this repo:</p>
          {#each scanResult.available_skills as sk}
            <button class="btn btn-sm" onclick={() => { skillCommand = skillCommand.replace(/\s+--skill.*$/, '') + ' --skill ' + sk; scanSkill() }}>
              {sk}
            </button>
          {/each}
        </div>
      {:else}
        <div class="scan-result">
          <div class="scan-header">
            <h4>{scanResult.name}</h4>
            {#if scanResult.description}
              <p class="dim">{scanResult.description}</p>
            {/if}
          </div>

          <div class="scan-verdict">
            <span class="verdict-badge verdict-{(scanResult.verdict || '').toLowerCase()}">{scanResult.verdict}</span>
            {#if scanResult.source}
              <span class="dim">{scanResult.source}</span>
            {/if}
          </div>

          {#if scanResult.issues && scanResult.issues.length > 0}
            <div class="scan-issues">
              <h5>Issues</h5>
              {#each scanResult.issues as issue}
                <div class="issue-item"><AlertTriangle size={12} /> {issue}</div>
              {/each}
            </div>
          {/if}

          {#if scanResult.triggers && scanResult.triggers.length > 0}
            <div class="scan-triggers">
              <span class="dim">Triggers:</span>
              {#each scanResult.triggers as t}
                <span class="trigger-tag">{t}</span>
              {/each}
            </div>
          {/if}

          {#if scanResult.tier}
            <p class="dim">Suggested tier: {scanResult.tier}</p>
          {/if}

          <div class="install-actions">
            <label class="checkbox-label">
              <input type="checkbox" bind:checked={skillOverwrite} /> Overwrite if exists
            </label>
            <button class="btn btn-primary" onclick={installSkill} disabled={installing || scanResult.verdict === 'FAIL'}>
              {#if installing}
                <Loader2 size={14} class="spin" /> Installing...
              {:else}
                <Package size={14} /> Install
              {/if}
            </button>
          </div>
        </div>
      {/if}
    {/if}
  </Card>
</div>

<!-- Create file/dir modal -->
<Modal open={showCreateModal} onclose={() => showCreateModal = false}>
  <h3>Create {createType === 'dir' ? 'Directory' : 'File'}</h3>
  <div class="modal-form">
    <div class="form-group">
      <label>Name</label>
      <input class="input" bind:value={createName} placeholder={createType === 'dir' ? 'new-folder' : 'file.txt'} onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && createEntry()} />
    </div>
    {#if createPath}
      <p class="dim">In: {createPath}/</p>
    {/if}
    <div class="modal-actions">
      <button class="btn btn-primary" onclick={createEntry}>Create</button>
      <button class="btn" onclick={() => showCreateModal = false}>Cancel</button>
    </div>
  </div>
</Modal>

<!-- Upload modal -->
<Modal open={showUploadModal} onclose={() => showUploadModal = false}>
  <h3>Upload Files</h3>
  <div class="modal-form">
    <input type="file" multiple onchange={(e: Event) => uploadFiles = (e.target as HTMLInputElement).files} class="input" />
    {#if uploadFiles?.length}
      <p class="dim">{uploadFiles.length} file(s) selected</p>
    {/if}
    {#if currentPath}
      <p class="dim">To: {currentPath}/</p>
    {/if}
    <div class="modal-actions">
      <button class="btn btn-primary" onclick={handleUpload} disabled={uploading || !uploadFiles?.length}>
        {uploading ? 'Uploading...' : 'Upload'}
      </button>
      <button class="btn" onclick={() => showUploadModal = false}>Cancel</button>
    </div>
  </div>
</Modal>

<style>
  .home-view {
    width: 100%;
    padding: 8px 0;
    position: relative;
  }

  .home-view h2 { margin-bottom: 16px; }

  /* Two-column workspace layout */
  .workspace-layout {
    display: grid;
    grid-template-columns: minmax(200px, 0.7fr) 1.3fr;
    gap: 1rem;
    align-items: start;
  }

  .file-viewer-placeholder {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 3rem;
    color: var(--text-dim);
    gap: 8px;
    background: var(--bg-card);
    border: 1px dashed var(--border);
    border-radius: var(--radius, 8px);
    min-height: 200px;
  }

  .file-viewer-placeholder p {
    font-size: 0.85rem;
    margin: 0;
  }

  @media (max-width: 768px) {
    .workspace-layout {
      grid-template-columns: 1fr;
    }
  }

  .home-view h3 {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 8px;
  }

  .dim { color: var(--text-dim); font-size: 0.85rem; }

  /* Activity */
  .activity-bar {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .activity-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
    font-size: 0.82rem;
  }

  .activity-name { flex: 1; }
  .activity-elapsed {
    display: flex;
    align-items: center;
    gap: 3px;
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  .activity-badge {
    font-size: 0.68rem;
    padding: 1px 6px;
    border-radius: 10px;
    background: rgba(59, 130, 246, 0.12);
    color: var(--blue, #3b82f6);
  }

  /* Section header */
  .section-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 10px;
  }

  .section-header h3 { margin: 0; }

  .ws-actions {
    display: flex;
    gap: 4px;
  }

  /* Breadcrumbs */
  .breadcrumbs {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-bottom: 10px;
    font-size: 0.8rem;
    flex-wrap: wrap;
  }

  .breadcrumb {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    font-family: inherit;
    font-size: 0.8rem;
    padding: 2px 4px;
    border-radius: 4px;
  }

  .breadcrumb:hover { background: var(--bg-input); }
  .breadcrumb-sep { color: var(--text-dim); }

  /* File tree */
  .file-tree {
    display: flex;
    flex-direction: column;
    gap: 1px;
    overflow-y: auto;
    max-height: 70vh;
  }

  .tree-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px;
    border-radius: var(--radius, 8px);
    cursor: pointer;
    font-size: 0.82rem;
    transition: background 0.1s;
  }

  .tree-row:hover { background: var(--bg-input); }

  .file-row-main {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    cursor: pointer;
  }

  .file-name {
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .file-size {
    font-size: 0.72rem;
    color: var(--text-dim);
    flex-shrink: 0;
  }

  .protected-badge {
    font-size: 0.65rem;
    padding: 0 5px;
    border-radius: 8px;
    background: rgba(234, 179, 8, 0.12);
    color: var(--yellow, #eab308);
  }

  .btn-icon {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    padding: 4px;
    border-radius: 4px;
    opacity: 0;
    transition: opacity 0.15s;
  }

  .tree-row:hover .btn-icon { opacity: 1; }
  .btn-icon:hover { color: var(--red, #c4392a); background: var(--bg-input); }

  /* File viewer */
  .file-meta {
    display: flex;
    gap: 8px;
    font-size: 0.75rem;
    color: var(--text-dim);
    margin-bottom: 10px;
  }

  .file-editor {
    resize: vertical;
    min-height: 400px;
    height: calc(100vh - 300px);
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.82rem;
  }

  .file-content {
    white-space: pre-wrap;
    word-break: break-word;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.8rem;
    line-height: 1.5;
    max-height: 500px;
    overflow-y: auto;
    padding: 8px 12px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
  }

  /* JSON viewer */
  .json-viewer {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.78rem;
    line-height: 1.6;
    max-height: 500px;
    overflow-y: auto;
    padding: 8px 12px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
  }

  .json-toggle {
    cursor: pointer;
    color: var(--accent);
  }

  .json-type { color: var(--text-dim); font-size: 0.72rem; }
  .json-key { color: var(--blue, #3b82f6); }
  .json-str { color: var(--green, #3d8b3d); }
  .json-num { color: var(--yellow, #eab308); }
  .json-bool { color: var(--accent); }
  .json-null { color: var(--text-dim); font-style: italic; }

  .json-children {
    border-left: 1px solid var(--border);
    padding-left: 8px;
  }

  .json-entry { padding: 1px 0; }

  /* JSONL */
  .jsonl-viewer {
    max-height: 500px;
    overflow-y: auto;
  }

  .jsonl-line {
    display: flex;
    gap: 8px;
    padding: 4px 0;
    border-bottom: 1px solid var(--border);
  }

  .jsonl-num {
    color: var(--text-dim);
    font-size: 0.72rem;
    width: 30px;
    text-align: right;
    flex-shrink: 0;
  }

  .jsonl-content {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.78rem;
    white-space: pre-wrap;
    word-break: break-word;
    flex: 1;
  }

  /* CSV */
  .csv-table-wrap {
    max-height: 500px;
    overflow: auto;
  }

  .csv-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.8rem;
  }

  .csv-table th, .csv-table td {
    padding: 4px 8px;
    border: 1px solid var(--border);
    text-align: left;
  }

  .csv-table th {
    background: var(--bg-input);
    font-weight: 500;
    position: sticky;
    top: 0;
  }

  /* Drag overlay */
  .drag-overlay {
    position: absolute;
    inset: 0;
    background: rgba(59, 130, 246, 0.1);
    border: 2px dashed var(--accent);
    border-radius: var(--radius, 8px);
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    z-index: 10;
    color: var(--accent);
    gap: 8px;
  }

  /* Teach */
  .teach-toggles {
    display: flex;
    gap: 16px;
    margin-bottom: 12px;
  }

  .radio-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.82rem;
    cursor: pointer;
  }

  .content-counter {
    font-size: 0.72rem;
    color: var(--text-dim);
    font-weight: 400;
  }

  .teach-result {
    margin-top: 12px;
    padding: 8px 12px;
    background: rgba(61, 139, 61, 0.08);
    border-radius: var(--radius, 8px);
    font-size: 0.82rem;
  }

  .teach-memories {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .teach-mem {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    font-size: 0.8rem;
  }

  /* Skill store */
  .scan-pick {
    margin-top: 12px;
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    align-items: center;
  }

  .scan-result {
    margin-top: 12px;
    padding: 12px;
    background: var(--bg-input);
    border-radius: var(--radius, 8px);
  }

  .scan-header h4 { margin: 0 0 4px; }

  .scan-verdict {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 8px 0;
  }

  .verdict-badge {
    padding: 2px 10px;
    border-radius: 12px;
    font-size: 0.72rem;
    font-weight: 600;
  }

  .verdict-pass { background: rgba(61, 139, 61, 0.15); color: var(--green, #3d8b3d); }
  .verdict-warn { background: rgba(234, 179, 8, 0.15); color: var(--yellow, #eab308); }
  .verdict-fail { background: rgba(196, 57, 42, 0.15); color: var(--red, #c4392a); }

  .scan-issues {
    margin: 8px 0;
  }

  .scan-issues h5 { margin-bottom: 4px; font-size: 0.8rem; }

  .issue-item {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.8rem;
    color: var(--yellow, #eab308);
    padding: 2px 0;
  }

  .scan-triggers {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    margin: 8px 0;
  }

  .trigger-tag {
    padding: 1px 8px;
    border-radius: 10px;
    font-size: 0.72rem;
    background: var(--bg-card);
    border: 1px solid var(--border);
  }

  .install-actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 12px;
    gap: 8px;
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

  /* Form */
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

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.82rem;
    cursor: pointer;
  }

  .modal-form { margin-top: 12px; }
  .modal-actions { display: flex; gap: 8px; margin-top: 16px; }

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

  :global(.markdown-body) {
    font-size: 0.88rem;
    line-height: 1.7;
    color: var(--text);
  }

  :global(.markdown-body h1),
  :global(.markdown-body h2),
  :global(.markdown-body h3) {
    margin: 1em 0 0.5em;
  }

  :global(.markdown-body h1) {
    font-size: 1.4rem;
    border-bottom: 1px solid var(--border);
    padding-bottom: 0.3em;
  }

  :global(.markdown-body h2) { font-size: 1.2rem; }
  :global(.markdown-body h3) { font-size: 1.05rem; }
  :global(.markdown-body p) { margin: 0.5em 0; }

  :global(.markdown-body pre) {
    background: var(--bg-input);
    padding: 12px;
    border-radius: 6px;
    overflow-x: auto;
  }

  :global(.markdown-body code) {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.85em;
  }

  :global(.markdown-body :not(pre) > code) {
    background: var(--bg-input);
    padding: 2px 5px;
    border-radius: 3px;
  }

  :global(.markdown-body ul),
  :global(.markdown-body ol) {
    padding-left: 1.5em;
    margin: 0.5em 0;
  }

  :global(.markdown-body blockquote) {
    border-left: 3px solid var(--border);
    padding-left: 1em;
    color: var(--text-dim);
    margin: 0.5em 0;
  }

  :global(.markdown-body table) {
    border-collapse: collapse;
    width: 100%;
    margin: 0.5em 0;
  }

  :global(.markdown-body th),
  :global(.markdown-body td) {
    border: 1px solid var(--border);
    padding: 6px 10px;
    text-align: left;
    font-size: 0.82rem;
  }

  :global(.markdown-body img) {
    max-width: 100%;
    border-radius: 6px;
  }

  :global(.markdown-body a) { color: var(--accent); }
</style>
