<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import {
    Folder, FolderOpen, File, FileText, FilePlus, FolderPlus,
    Trash2, Download, Upload, Save, Eye, Pencil, ChevronRight, ChevronDown,
    X, ArrowLeft, Loader2, RefreshCw, MoreVertical
  } from 'lucide-svelte'
  import Modal from '../components/shared/Modal.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'

  // --- Workspace types ---
  interface WsEntry {
    name: string
    is_dir: boolean
    size: number
    mod_time?: number
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
    mod_time?: number
    editable: boolean
    content?: string
    message?: string
  }

  // --- State ---
  let currentPath = $state('')
  let dirEntries = $state<WsEntry[]>([])
  let protectedDirs = $state<string[]>([])
  let loadingDir = $state(false)

  // Sidebar tree
  let expandedDirs = $state<Record<string, WsEntry[]>>({})
  let sidebarRootEntries = $state<WsEntry[]>([])

  // File modal
  let showFileModal = $state(false)
  let viewingFile = $state<WsFile | null>(null)
  let viewingPath = $state('')
  let fileEditMode = $state(false)
  let fileEditContent = $state('')
  let savingFile = $state(false)
  let mdPreview = $state(true)

  // JSON viewer
  let jsonExpanded = $state<Record<string, boolean>>({})
  let jsonTreeView = $state(false)

  // Context menu
  let ctxMenu = $state<{ x: number; y: number; entry: WsEntry; path: string } | null>(null)

  // Create file/dir
  let showCreateModal = $state(false)
  let createType = $state<'file' | 'dir'>('file')
  let createName = $state('')

  // Upload
  let showUploadModal = $state(false)
  let uploadFiles = $state<FileList | null>(null)
  let uploading = $state(false)
  let dragOver = $state(false)

  // Breadcrumbs
  let breadcrumbs = $derived(() => {
    if (!currentPath) return []
    const parts = currentPath.split('/')
    const crumbs: { name: string; path: string }[] = []
    for (let i = 0; i < parts.length; i++) {
      crumbs.push({ name: parts[i], path: parts.slice(0, i + 1).join('/') })
    }
    return crumbs
  })

  // Sorted entries: dirs first, then files
  let sortedEntries = $derived(
    [...dirEntries].sort((a, b) => {
      if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
  )

  // --- Helpers ---
  function fileExt(name: string): string {
    const idx = name.lastIndexOf('.')
    return idx >= 0 ? name.slice(idx + 1).toLowerCase() : ''
  }

  function isMarkdown(name: string): boolean {
    return ['md', 'markdown'].includes(fileExt(name))
  }

  function isJson(name: string): boolean { return fileExt(name) === 'json' }
  function isJsonl(name: string): boolean { return fileExt(name) === 'jsonl' }
  function isCsv(name: string): boolean { return fileExt(name) === 'csv' }

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  }

  function formatDate(ts?: number): string {
    if (!ts) return ''
    const d = new Date(ts * 1000)
    const now = new Date()
    const isToday = d.toDateString() === now.toDateString()
    if (isToday) return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: d.getFullYear() !== now.getFullYear() ? 'numeric' : undefined })
  }


  function renderMd(text: string): string {
    return DOMPurify.sanitize(marked.parse(text) as string)
  }

  function isProtected(name: string): boolean {
    return protectedDirs.includes(name)
  }

  function parentPath(path: string): string {
    const idx = path.lastIndexOf('/')
    return idx > 0 ? path.slice(0, idx) : ''
  }

  // --- Directory loading ---
  async function loadDir(path: string) {
    loadingDir = true
    try {
      const data = await api<WsDir>(`/api/workspace?path=${encodeURIComponent(path)}`)
      if (data.type === 'directory') {
        dirEntries = data.entries || []
        currentPath = path
        if (data.protected) protectedDirs = data.protected
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to load directory', 'error')
    } finally {
      loadingDir = false
    }
  }

  async function loadSidebarRoot() {
    try {
      const data = await api<WsDir>('/api/workspace?path=')
      if (data.type === 'directory') {
        sidebarRootEntries = (data.entries || []).filter(e => e.is_dir).sort((a, b) => a.name.localeCompare(b.name))
        if (data.protected) protectedDirs = data.protected
      }
    } catch { /* silent */ }
  }

  async function toggleSidebarDir(path: string) {
    if (expandedDirs[path]) {
      delete expandedDirs[path]
      expandedDirs = { ...expandedDirs }
    } else {
      try {
        const data = await api<WsDir>(`/api/workspace?path=${encodeURIComponent(path)}`)
        if (data.type === 'directory') {
          expandedDirs[path] = (data.entries || []).filter(e => e.is_dir).sort((a, b) => a.name.localeCompare(b.name))
          expandedDirs = { ...expandedDirs }
        }
      } catch { /* silent */ }
    }
  }

  function navigateToDir(path: string) {
    loadDir(path)
  }

  // --- File operations ---
  async function openFile(path: string) {
    try {
      const data = await api<WsFile>(`/api/workspace?path=${encodeURIComponent(path)}`)
      if (data.type === 'file') {
        viewingFile = data
        viewingPath = path
        fileEditMode = false
        fileEditContent = data.content || ''
        mdPreview = isMarkdown(data.name)
        jsonTreeView = false
        jsonExpanded = {}
        showFileModal = true
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to open file', 'error')
    }
  }

  async function saveFile() {
    if (!viewingPath) return
    savingFile = true
    try {
      await api('PUT', `/api/workspace?path=${encodeURIComponent(viewingPath)}`, { content: fileEditContent })
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
    try {
      await api(`/api/workspace?path=${encodeURIComponent(path)}`, { method: 'DELETE' })
      toasts.show('Deleted', 'success')
      if (viewingPath === path) { showFileModal = false; viewingFile = null }
      loadDir(currentPath)
    } catch (e: any) {
      toasts.show(e.error || 'Failed to delete', 'error')
    }
  }

  // --- Context menu ---
  function handleContextMenu(e: MouseEvent, entry: WsEntry) {
    e.preventDefault()
    const fullPath = currentPath ? `${currentPath}/${entry.name}` : entry.name
    ctxMenu = { x: e.clientX, y: e.clientY, entry, path: fullPath }
  }

  function closeContextMenu() {
    ctxMenu = null
  }

  function ctxOpen() {
    if (!ctxMenu) return
    if (ctxMenu.entry.is_dir) {
      navigateToDir(ctxMenu.path)
    } else {
      openFile(ctxMenu.path)
    }
    closeContextMenu()
  }

  function ctxEdit() {
    if (!ctxMenu || ctxMenu.entry.is_dir) return
    openFile(ctxMenu.path).then(() => {
      setTimeout(() => { fileEditMode = true }, 100)
    })
    closeContextMenu()
  }

  function ctxDownload() {
    if (!ctxMenu) return
    window.open(`/api/workspace?path=${encodeURIComponent(ctxMenu.path)}`, '_blank')
    closeContextMenu()
  }

  function ctxDelete() {
    if (!ctxMenu) return
    if (confirm(`Delete "${ctxMenu.entry.name}"?`)) {
      deleteEntry(ctxMenu.path)
    }
    closeContextMenu()
  }

  // --- Create ---
  function openCreateDialog(type: 'file' | 'dir') {
    createType = type
    createName = ''
    showCreateModal = true
  }

  async function createEntry() {
    if (!createName.trim()) return
    const path = currentPath ? `${currentPath}/${createName.trim()}` : createName.trim()
    try {
      if (createType === 'dir') {
        await api(`/api/workspace?path=${encodeURIComponent(path + '/.keep')}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ content: '' })
        })
      } else {
        await api(`/api/workspace?path=${encodeURIComponent(path)}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ content: '' })
        })
      }
      toasts.show(`${createType === 'dir' ? 'Directory' : 'File'} created`, 'success')
      showCreateModal = false
      loadDir(currentPath)
      loadSidebarRoot()
    } catch (e: any) {
      toasts.show(e.error || 'Failed to create', 'error')
    }
  }

  // --- Upload ---
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

  function handleDragOver(e: DragEvent) { e.preventDefault(); dragOver = true }
  function handleDragLeave() { dragOver = false }
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

  // --- JSONL / CSV ---
  function parseJsonl(content: string): any[] {
    return content.split('\n').filter(l => l.trim()).map(l => {
      try { return JSON.parse(l) } catch { return { _raw: l } }
    })
  }

  function parseCsv(content: string): { headers: string[]; rows: string[][] } {
    const lines = content.split('\n').filter(l => l.trim())
    if (lines.length === 0) return { headers: [], rows: [] }
    const headers = lines[0].split(',').map(h => h.trim())
    const rows = lines.slice(1).map(l => l.split(',').map(c => c.trim()))
    return { headers, rows }
  }

  // --- Events ---
  function handleOpenFile(e: CustomEvent<{ path: string }>) {
    openFile(e.detail.path)
  }

  async function handleOpenDir(e: CustomEvent<{ path: string }>) {
    const targetPath = e.detail.path
    loadDir(targetPath)

    // Expand all parent directories in the sidebar tree.
    const segments = targetPath.split('/').filter(Boolean)
    let current = ''
    for (const seg of segments) {
      current = current ? `${current}/${seg}` : seg
      if (!expandedDirs[current]) {
        try {
          const data = await api<WsDir>(`/api/workspace?path=${encodeURIComponent(current)}`)
          if (data.type === 'directory') {
            expandedDirs[current] = (data.entries || []).filter(e => e.is_dir).sort((a, b) => a.name.localeCompare(b.name))
          }
        } catch { /* silent */ }
      }
    }
    expandedDirs = { ...expandedDirs }
  }

  function handleGlobalClick() {
    if (ctxMenu) closeContextMenu()
  }

  onMount(() => {
    loadDir('')
    loadSidebarRoot()
    window.addEventListener('alf:open-file', handleOpenFile as EventListener)
    window.addEventListener('alf:open-dir', handleOpenDir as EventListener)
    window.addEventListener('click', handleGlobalClick)
  })

  onDestroy(() => {
    window.removeEventListener('alf:open-file', handleOpenFile as EventListener)
    window.removeEventListener('alf:open-dir', handleOpenDir as EventListener)
    window.removeEventListener('click', handleGlobalClick)
  })
</script>

<div class="ws-root" ondragover={handleDragOver} ondragleave={handleDragLeave} ondrop={handleDrop} role="main">
  {#if dragOver}
    <div class="drag-overlay">
      <Upload size={48} />
      <p>Drop files to upload</p>
    </div>
  {/if}

  <!-- Header bar -->
  <div class="ws-header">
    <span class="ws-title">Files</span>
    <div class="ws-toolbar">
      <button class="ws-btn" onclick={() => openCreateDialog('file')} title="New file"><FilePlus size={15} /></button>
      <button class="ws-btn" onclick={() => openCreateDialog('dir')} title="New folder"><FolderPlus size={15} /></button>
      <button class="ws-btn" onclick={() => { showUploadModal = true }} title="Upload"><Upload size={15} /></button>
      <button class="ws-btn" onclick={() => { loadDir(currentPath); loadSidebarRoot() }} title="Refresh"><RefreshCw size={15} /></button>
    </div>
  </div>

  <div class="ws-layout">
    <!-- Left sidebar: folder tree -->
    <div class="ws-sidebar">
      <div class="sidebar-tree">
        <div
          class="sidebar-item root-item"
          class:active={currentPath === ''}
          onclick={() => navigateToDir('')}
          role="button"
          tabindex="0"
          onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && navigateToDir('')}
        >
          <FolderOpen size={14} />
          <span>data</span>
        </div>

        {#snippet sidebarNode(entries: WsEntry[], parentPath: string, depth: number)}
          {#each entries as entry}
            {@const fullPath = parentPath ? `${parentPath}/${entry.name}` : entry.name}
            {@const isExpanded = !!expandedDirs[fullPath]}
            <div
              class="sidebar-item"
              class:active={currentPath === fullPath}
              style="padding-left:{(depth + 1) * 14 + 8}px"
              onclick={() => { navigateToDir(fullPath); if (!isExpanded) toggleSidebarDir(fullPath) }}
              role="button"
              tabindex="0"
              onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && navigateToDir(fullPath)}
            >
              <button class="expand-btn" onclick={(e: MouseEvent) => { e.stopPropagation(); toggleSidebarDir(fullPath) }}>
                {#if isExpanded}
                  <ChevronDown size={12} />
                {:else}
                  <ChevronRight size={12} />
                {/if}
              </button>
              {#if isExpanded}
                <FolderOpen size={14} />
              {:else}
                <Folder size={14} />
              {/if}
              <span>{entry.name}</span>
            </div>
            {#if isExpanded && expandedDirs[fullPath]}
              {@render sidebarNode(expandedDirs[fullPath], fullPath, depth + 1)}
            {/if}
          {/each}
        {/snippet}
        {@render sidebarNode(sidebarRootEntries, '', 0)}
      </div>
    </div>

    <!-- Right panel: file list -->
    <div class="ws-main">
      <!-- Breadcrumbs -->
      <div class="ws-breadcrumbs">
        <button class="crumb" onclick={() => navigateToDir('')}>data</button>
        {#each breadcrumbs() as crumb}
          <span class="crumb-sep">&rsaquo;</span>
          <button class="crumb" onclick={() => navigateToDir(crumb.path)}>{crumb.name}</button>
        {/each}
      </div>

      <!-- File table -->
      {#if loadingDir}
        <div class="ws-loading"><Loader2 size={20} class="spin" /> Loading...</div>
      {:else}
        <table class="file-table">
          <thead>
            <tr>
              <th class="col-name">Name</th>
              <th class="col-size">Size</th>
              <th class="col-date">Date</th>
            </tr>
          </thead>
          <tbody>
            {#if currentPath}
              <tr class="file-row back-row" onclick={() => navigateToDir(parentPath(currentPath))}>
                <td colspan="3">
                  <div class="cell-name">
                    <ArrowLeft size={14} />
                    <span>Back to parent folder</span>
                  </div>
                </td>
              </tr>
            {/if}
            {#each sortedEntries as entry}
              {@const fullPath = currentPath ? `${currentPath}/${entry.name}` : entry.name}
              <tr
                class="file-row"
                onclick={() => entry.is_dir ? navigateToDir(fullPath) : openFile(fullPath)}
                oncontextmenu={(e) => handleContextMenu(e, entry)}
              >
                <td>
                  <div class="cell-name">
                    {#if entry.is_dir}
                      <Folder size={15} class="icon-folder" />
                    {:else}
                      <File size={15} class="icon-file" />
                    {/if}
                    <span>{entry.name}</span>
                    {#if entry.is_dir && isProtected(entry.name)}
                      <span class="badge-protected">protected</span>
                    {/if}
                  </div>
                </td>
                <td class="cell-size">{entry.is_dir ? '' : formatSize(entry.size)}</td>
                <td class="cell-date">{formatDate(entry.mod_time)}</td>
              </tr>
            {/each}
            {#if sortedEntries.length === 0 && !currentPath}
              <tr><td colspan="3" class="empty-msg">Empty workspace</td></tr>
            {/if}
          </tbody>
        </table>
      {/if}
    </div>
  </div>
</div>

<!-- Context menu -->
{#if ctxMenu}
  <div class="ctx-menu" style="left:{ctxMenu.x}px;top:{ctxMenu.y}px">
    <button class="ctx-item" onclick={ctxOpen}>
      <Eye size={13} /> Open
    </button>
    {#if !ctxMenu.entry.is_dir}
      <button class="ctx-item" onclick={ctxEdit}>
        <Pencil size={13} /> Edit
      </button>
      <button class="ctx-item" onclick={ctxDownload}>
        <Download size={13} /> Download
      </button>
    {/if}
    {#if !(ctxMenu.entry.is_dir && isProtected(ctxMenu.entry.name))}
      <div class="ctx-sep"></div>
      <button class="ctx-item ctx-danger" onclick={ctxDelete}>
        <Trash2 size={13} /> Delete
      </button>
    {/if}
  </div>
{/if}

<!-- File viewer modal -->
<Modal open={showFileModal} onclose={() => { showFileModal = false; viewingFile = null }} wide>
  {#if viewingFile}
    <div class="modal-file-header">
      <h3><FileText size={16} /> {viewingFile.name}</h3>
      <div class="modal-file-actions">
        {#if viewingFile.editable && viewingFile.content !== undefined}
          {#if fileEditMode}
            <button class="ws-btn primary" onclick={saveFile} disabled={savingFile}>
              <Save size={13} /> {savingFile ? 'Saving...' : 'Save'}
            </button>
            <button class="ws-btn" onclick={() => { fileEditMode = false; fileEditContent = viewingFile!.content || '' }}>Cancel</button>
          {:else}
            <button class="ws-btn" onclick={() => fileEditMode = true}>
              <Pencil size={13} /> Edit
            </button>
          {/if}
        {/if}
        {#if isMarkdown(viewingFile.name) && !fileEditMode}
          <button class="ws-btn" onclick={() => mdPreview = !mdPreview}>
            {mdPreview ? 'Source' : 'Preview'}
          </button>
        {/if}
        {#if isJson(viewingFile.name) && !fileEditMode}
          <button class="ws-btn" onclick={() => jsonTreeView = !jsonTreeView}>
            {jsonTreeView ? 'Raw' : 'Tree'}
          </button>
        {/if}
      </div>
    </div>

    <div class="file-meta">
      <span>{formatSize(viewingFile.size)}</span>
      {#if viewingFile.mod_time}<span class="dim">Modified {formatDate(viewingFile.mod_time)}</span>{/if}
      {#if !viewingFile.editable}<span class="dim">read-only</span>{/if}
    </div>

    <div class="modal-file-body">
      {#if viewingFile.message}
        <p class="dim">{viewingFile.message}</p>
        {#if viewingFile.message.includes('Binary')}
          <a href={`/api/workspace?path=${encodeURIComponent(viewingPath)}`} download class="ws-btn" style="margin-top:8px">
            <Download size={13} /> Download
          </a>
        {/if}
      {:else if viewingFile.content !== undefined}
        {#if fileEditMode}
          <textarea class="file-editor" bind:value={fileEditContent} rows={24}></textarea>
        {:else if isMarkdown(viewingFile.name) && mdPreview}
          <div class="markdown-body">{@html renderMd(viewingFile.content)}</div>
        {:else if isJson(viewingFile.name)}
          {#if jsonTreeView}
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
                      {jsonExpanded[path] ? '▾' : '▸'} <span class="json-type">array[{val.length}]</span>
                    </span>
                    {#if jsonExpanded[path]}
                      <div class="json-children" style="margin-left:{Math.min(depth, 4) * 14}px">
                        {#each val as item, i}
                          <div class="json-entry"><span class="json-key">{i}:</span> {@render jsonNode(item, `${path}.${i}`, depth + 1)}</div>
                        {/each}
                      </div>
                    {/if}
                  {:else if typeof val === 'object'}
                    <span class="json-toggle" onclick={() => toggleJsonKey(path)} role="button" tabindex="0" onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && toggleJsonKey(path)}>
                      {jsonExpanded[path] ? '▾' : '▸'} <span class="json-type">object&lbrace;{Object.keys(val).length}&rbrace;</span>
                    </span>
                    {#if jsonExpanded[path]}
                      <div class="json-children" style="margin-left:{Math.min(depth, 4) * 14}px">
                        {#each Object.entries(val) as [k, v]}
                          <div class="json-entry"><span class="json-key">"{k}":</span> {@render jsonNode(v, `${path}.${k}`, depth + 1)}</div>
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
          <div class="csv-wrap">
            <table class="csv-table">
              <thead><tr>{#each headers as h}<th>{h}</th>{/each}</tr></thead>
              <tbody>{#each rows as row}<tr>{#each row as cell}<td>{cell}</td>{/each}</tr>{/each}</tbody>
            </table>
          </div>
        {:else}
          <pre class="file-content">{viewingFile.content}</pre>
        {/if}
      {/if}
    </div>
  {/if}
</Modal>

<!-- Create modal -->
<Modal open={showCreateModal} onclose={() => showCreateModal = false}>
  <h3>Create {createType === 'dir' ? 'Folder' : 'File'}</h3>
  <div class="modal-form">
    <div class="form-group">
      <label>Name</label>
      <input class="input" bind:value={createName} placeholder={createType === 'dir' ? 'new-folder' : 'file.txt'} onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && createEntry()} />
    </div>
    {#if currentPath}
      <p class="dim">In: {currentPath}/</p>
    {/if}
    <div class="modal-actions">
      <button class="ws-btn primary" onclick={createEntry}>Create</button>
      <button class="ws-btn" onclick={() => showCreateModal = false}>Cancel</button>
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
      <button class="ws-btn primary" onclick={handleUpload} disabled={uploading || !uploadFiles?.length}>
        {uploading ? 'Uploading...' : 'Upload'}
      </button>
      <button class="ws-btn" onclick={() => showUploadModal = false}>Cancel</button>
    </div>
  </div>
</Modal>

<style>
  .ws-root {
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    position: relative;
    overflow: hidden;
  }

  /* Header */
  .ws-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 16px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .ws-title {
    font-size: var(--font-md, 15px);
    font-weight: 600;
  }

  .ws-toolbar {
    display: flex;
    gap: 4px;
  }

  /* Two-column layout */
  .ws-layout {
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  /* Sidebar */
  .ws-sidebar {
    width: 220px;
    min-width: 180px;
    border-right: 1px solid var(--border);
    overflow-y: auto;
    padding: 8px 0;
    flex-shrink: 0;
  }

  .sidebar-tree {
    display: flex;
    flex-direction: column;
  }

  .sidebar-item {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 5px 12px;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    user-select: none;
    border-radius: 0;
    transition: background 0.1s;
  }

  .sidebar-item:hover { background: var(--bg-input); }
  .sidebar-item.active { background: var(--accent); color: var(--on-accent); }
  .sidebar-item.active :global(svg) { color: var(--on-accent); }

  .root-item { padding-left: 12px; font-weight: 500; }

  .expand-btn {
    background: none;
    border: none;
    padding: 0;
    cursor: pointer;
    color: inherit;
    display: flex;
    align-items: center;
  }

  /* Main panel */
  .ws-main {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    min-width: 0;
  }

  .ws-breadcrumbs {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 10px 16px;
    font-size: var(--font-sm, 13px);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .crumb {
    background: none;
    border: none;
    color: var(--text);
    cursor: pointer;
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    padding: 2px 4px;
    border-radius: 4px;
  }

  .crumb:hover { background: var(--bg-input); }
  .crumb-sep { color: var(--text-dim); }

  .ws-loading {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 24px;
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
  }

  /* File table */
  .file-table {
    width: 100%;
    border-collapse: collapse;
    table-layout: fixed;
  }

  .file-table thead {
    position: sticky;
    top: 0;
    z-index: 1;
  }

  .file-table th {
    text-align: left;
    padding: 8px 16px;
    font-size: var(--font-sm, 13px);
    font-weight: 600;
    color: var(--text-dim);
    background: var(--bg);
    border-bottom: 1px solid var(--border);
  }

  .col-name { width: auto; }
  .col-size { width: 90px; }
  .col-date { width: 120px; }

  .file-row {
    cursor: pointer;
    transition: background 0.1s;
  }

  .file-row:hover { background: var(--bg-input); }

  .file-row td {
    padding: 7px 16px;
    font-size: var(--font-sm, 13px);
    border-bottom: 1px solid color-mix(in srgb, var(--border) 40%, transparent);
  }

  .back-row td { color: var(--accent); }

  .cell-name {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }

  .cell-name span {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .cell-size, .cell-date {
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
  }

  :global(.icon-folder) { color: var(--accent); }
  :global(.icon-file) { color: var(--text-dim); }

  .badge-protected {
    font-size: var(--font-xs, 11px);
    padding: 0 5px;
    border-radius: 8px;
    background: color-mix(in srgb, var(--yellow) 12%, transparent);
    color: var(--yellow, #eab308);
    flex-shrink: 0;
  }

  .empty-msg {
    text-align: center;
    color: var(--text-dim);
    padding: 24px !important;
  }

  /* Context menu */
  .ctx-menu {
    position: fixed;
    z-index: 1000;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 4px;
    min-width: 160px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.15);
  }

  .ctx-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 7px 12px;
    background: none;
    border: none;
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    border-radius: 5px;
    text-align: left;
  }

  .ctx-item:hover { background: var(--bg-input); }
  .ctx-danger { color: var(--red, #c4392a); }
  .ctx-sep { height: 1px; background: var(--border); margin: 4px 8px; }

  /* Drag overlay */
  .drag-overlay {
    position: absolute;
    inset: 0;
    background: color-mix(in srgb, var(--sapphire) 10%, transparent);
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

  /* Shared button */
  .ws-btn {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 5px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg-card);
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    transition: background 0.15s;
  }

  .ws-btn:hover { background: var(--bg-input); }
  .ws-btn:disabled { opacity: 0.5; cursor: not-allowed; }
  .ws-btn.primary { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
  .ws-btn.primary:hover { opacity: 0.9; }

  /* File modal */
  .modal-file-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 8px;
  }

  .modal-file-header h3 {
    display: flex;
    align-items: center;
    gap: 6px;
    margin: 0;
  }

  .modal-file-actions {
    display: flex;
    gap: 6px;
  }

  .file-meta {
    display: flex;
    gap: 8px;
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    margin-bottom: 10px;
  }

  .modal-file-body {
    max-height: 70vh;
    overflow-y: auto;
  }

  .file-editor {
    width: 100%;
    resize: vertical;
    min-height: 300px;
    font-family: 'JetBrains Mono', monospace;
    font-size: var(--font-sm, 13px);
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg-input);
    color: var(--text);
  }

  .file-content {
    white-space: pre-wrap;
    word-break: break-word;
    font-family: 'JetBrains Mono', monospace;
    font-size: var(--font-sm, 13px);
    line-height: 1.5;
    padding: 8px 12px;
    background: var(--bg-input);
    border-radius: 6px;
    overflow-x: auto;
  }

  /* JSON */
  .json-viewer {
    font-family: 'JetBrains Mono', monospace;
    font-size: var(--font-sm, 13px);
    line-height: 1.6;
    padding: 8px 12px;
    background: var(--bg-input);
    border-radius: 6px;
    max-height: 60vh;
    overflow-y: auto;
  }

  .json-toggle { cursor: pointer; color: var(--accent); }
  .json-type { color: var(--text-dim); font-size: var(--font-xs, 11px); }
  .json-key { color: var(--blue, #3b82f6); }
  .json-str { color: var(--green, #3d8b3d); }
  .json-num { color: var(--yellow, #eab308); }
  .json-bool { color: var(--accent); }
  .json-null { color: var(--text-dim); font-style: italic; }
  .json-children { border-left: 1px solid var(--border); padding-left: 8px; }
  .json-entry { padding: 1px 0; }

  /* JSONL */
  .jsonl-viewer { max-height: 60vh; overflow-y: auto; }
  .jsonl-line { display: flex; gap: 8px; padding: 4px 0; border-bottom: 1px solid var(--border); }
  .jsonl-num { color: var(--text-dim); font-size: var(--font-xs, 11px); width: 30px; text-align: right; flex-shrink: 0; }
  .jsonl-content { font-family: 'JetBrains Mono', monospace; font-size: var(--font-sm, 13px); white-space: pre-wrap; word-break: break-word; flex: 1; }

  /* CSV */
  .csv-wrap { max-height: 60vh; overflow: auto; }
  .csv-table { width: 100%; border-collapse: collapse; font-size: var(--font-sm, 13px); }
  .csv-table th, .csv-table td { padding: 4px 8px; border: 1px solid var(--border); text-align: left; }
  .csv-table th { background: var(--bg-input); font-weight: 500; position: sticky; top: 0; }

  /* Markdown */
  :global(.markdown-body) { font-size: var(--font-sm, 13px); line-height: 1.7; color: var(--text); overflow-wrap: break-word; }
  :global(.markdown-body h1), :global(.markdown-body h2), :global(.markdown-body h3) { margin: 1em 0 0.5em; }
  :global(.markdown-body h1) { font-size: var(--font-xl, 24px); border-bottom: 1px solid var(--border); padding-bottom: 0.3em; }
  :global(.markdown-body h2) { font-size: var(--font-lg, 18px); }
  :global(.markdown-body h3) { font-size: var(--font-md, 15px); }
  :global(.markdown-body p) { margin: 0.5em 0; }
  :global(.markdown-body pre) { background: var(--bg-input); padding: 12px; border-radius: 6px; overflow-x: auto; }
  :global(.markdown-body code) { font-family: 'JetBrains Mono', monospace; font-size: 0.85em; }
  :global(.markdown-body :not(pre) > code) { background: var(--bg-input); padding: 2px 5px; border-radius: 3px; }
  :global(.markdown-body ul), :global(.markdown-body ol) { padding-left: 1.5em; margin: 0.5em 0; }
  :global(.markdown-body blockquote) { border-left: 3px solid var(--border); padding-left: 1em; color: var(--text-dim); margin: 0.5em 0; }
  :global(.markdown-body table) { border-collapse: collapse; width: 100%; margin: 0.5em 0; }
  :global(.markdown-body th), :global(.markdown-body td) { border: 1px solid var(--border); padding: 6px 10px; text-align: left; font-size: var(--font-sm, 13px); }
  :global(.markdown-body img) { max-width: 100%; border-radius: 6px; }
  :global(.markdown-body a) { color: var(--accent); }

  /* Form */
  .dim { color: var(--text-dim); font-size: var(--font-sm, 13px); }
  .modal-form { margin-top: 12px; }
  .modal-actions { display: flex; gap: 8px; margin-top: 16px; }

  @media (max-width: 768px) {
    .ws-sidebar { display: none; }
    .col-date { display: none; }
  }
</style>
