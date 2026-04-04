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
    media?: string
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
    media?: string
    url?: string
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
    window.open(`/api/workspace?path=${encodeURIComponent(ctxMenu.path)}&download=1`, '_blank')
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

<div class="file-browser" ondragover={handleDragOver} ondragleave={handleDragLeave} ondrop={handleDrop} role="main">
  {#if dragOver}
    <div class="drag-overlay">
      <Upload size={48} />
      <p>Drop files to upload</p>
    </div>
  {/if}

  <!-- Header bar -->
  <div class="file-browser-header">
    <span class="title">Files</span>
    <div class="toolbar">
      <button class="btn btn-sm" onclick={() => openCreateDialog('file')} title="New file"><FilePlus size={15} /></button>
      <button class="btn btn-sm" onclick={() => openCreateDialog('dir')} title="New folder"><FolderPlus size={15} /></button>
      <button class="btn btn-sm" onclick={() => { showUploadModal = true }} title="Upload"><Upload size={15} /></button>
      <button class="btn btn-sm" onclick={() => { loadDir(currentPath); loadSidebarRoot() }} title="Refresh"><RefreshCw size={15} /></button>
    </div>
  </div>

  <div class="file-browser-layout">
    <!-- Left sidebar: folder tree -->
    <div class="file-browser-sidebar">
      <div class="sidebar-tree">
        <div
          class="sidebar-item root"
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
    <div class="file-browser-main">
      <!-- Breadcrumbs -->
      <div class="breadcrumb">
        <button class="breadcrumb-btn" onclick={() => navigateToDir('')}>data</button>
        {#each breadcrumbs() as crumb}
          <span class="breadcrumb-sep">&rsaquo;</span>
          <button class="breadcrumb-btn" onclick={() => navigateToDir(crumb.path)}>{crumb.name}</button>
        {/each}
      </div>

      <!-- File table -->
      {#if loadingDir}
        <div class="file-browser-loading"><Loader2 size={20} class="spin" /> Loading...</div>
      {:else}
        <table class="data-table">
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
                    {:else if entry.media === 'image'}
                      <img src={`/api/workspace?path=${encodeURIComponent(fullPath)}&download=1`} alt="" class="file-thumb" />
                    {:else}
                      <File size={15} class="icon-file" />
                    {/if}
                    <span>{entry.name}</span>
                    {#if entry.is_dir && isProtected(entry.name)}
                      <span class="tag tag-warning">protected</span>
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
  <div class="dropdown-menu" style="left:{ctxMenu.x}px;top:{ctxMenu.y}px">
    <button class="dropdown-item" onclick={ctxOpen}>
      <Eye size={13} /> Open
    </button>
    {#if !ctxMenu.entry.is_dir}
      <button class="dropdown-item" onclick={ctxEdit}>
        <Pencil size={13} /> Edit
      </button>
      <button class="dropdown-item" onclick={ctxDownload}>
        <Download size={13} /> Download
      </button>
    {/if}
    {#if !(ctxMenu.entry.is_dir && isProtected(ctxMenu.entry.name))}
      <div class="dropdown-separator"></div>
      <button class="dropdown-item dropdown-item-danger" onclick={ctxDelete}>
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
            <button class="btn btn-primary btn-sm" onclick={saveFile} disabled={savingFile}>
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
            {mdPreview ? 'Source' : 'Preview'}
          </button>
        {/if}
        {#if isJson(viewingFile.name) && !fileEditMode}
          <button class="btn btn-sm" onclick={() => jsonTreeView = !jsonTreeView}>
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
      {#if viewingFile.media && viewingFile.url}
        <div class="media-preview">
          {#if viewingFile.media === 'image'}
            <img src={viewingFile.url} alt={viewingFile.name} />
          {:else if viewingFile.media === 'video'}
            <video controls src={viewingFile.url}></video>
          {:else if viewingFile.media === 'audio'}
            <audio controls src={viewingFile.url}></audio>
          {/if}
          <a href={viewingFile.url} download class="btn btn-sm" style="margin-top:8px">
            <Download size={13} /> Download
          </a>
        </div>
      {:else if viewingFile.message}
        <p class="dim">{viewingFile.message}</p>
        {#if viewingFile.message.includes('Binary')}
          <a href={`/api/workspace?path=${encodeURIComponent(viewingPath)}&download=1`} download class="btn btn-sm" style="margin-top:8px">
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
      <button class="btn btn-primary btn-sm" onclick={createEntry}>Create</button>
      <button class="btn btn-sm" onclick={() => showCreateModal = false}>Cancel</button>
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
      <button class="btn btn-primary btn-sm" onclick={handleUpload} disabled={uploading || !uploadFiles?.length}>
        {uploading ? 'Uploading...' : 'Upload'}
      </button>
      <button class="btn btn-sm" onclick={() => showUploadModal = false}>Cancel</button>
    </div>
  </div>
</Modal>

