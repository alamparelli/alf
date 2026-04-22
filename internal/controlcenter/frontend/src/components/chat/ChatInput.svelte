<script lang="ts">
  import { Paperclip, Send, Square, X, Sparkles } from 'lucide-svelte'
  import { api } from '../../lib/api'
  import { toasts } from '../../stores/toast.svelte'

  interface UploadedFile {
    upload_id: string
    file_name: string
    mime_type: string
    previewUrl?: string
  }

  interface Props {
    onSend: (message: string, mediaFiles: UploadedFile[], model: string) => void
    onStop?: () => void
    sending: boolean
    tiers?: { name: string; model: string; enabled?: boolean; force_command?: boolean }[]
    draft?: string
    onDraftChange?: (text: string) => void
    selectedModel?: string
    onModelChange?: (model: string) => void
    activeSkills?: string[]
    onDismissSkill?: (name: string) => void
    convId?: string
  }

  let { onSend, onStop, sending, tiers = [], draft = '', onDraftChange, selectedModel: selectedModelProp = '', onModelChange, activeSkills = [], onDismissSkill, convId }: Props = $props()

  let text = $state(draft)
  let files = $state<UploadedFile[]>([])
  let uploading = $state(false)
  let selectedModel = $state(selectedModelProp)
  let textarea: HTMLTextAreaElement
  let fileInput: HTMLInputElement
  let dragOver = $state(false)
  let tierDropdownOpen = $state(false)
  let tierBtnEl: HTMLButtonElement

  // Sync text and model when props change (tab switch)
  $effect(() => {
    text = draft
  })
  $effect(() => {
    selectedModel = selectedModelProp
  })

  // Auto-focus composer when opening/switching a chat so the user can type immediately.
  $effect(() => {
    void convId
    if (!textarea) return
    requestAnimationFrame(() => textarea?.focus())
  })

  function selectTier(name: string) {
    selectedModel = name
    onModelChange?.(name)
    tierDropdownOpen = false
  }

  function handleTierClickOutside(e: MouseEvent) {
    if (tierBtnEl && !tierBtnEl.contains(e.target as Node)) {
      tierDropdownOpen = false
    }
  }

  // Notify parent of text changes
  function onInput() {
    if (onDraftChange) onDraftChange(text)
  }

  // Slash commands
  let showCommands = $state(false)
  let commandFilter = $state('')
  let selectedCommandIdx = $state(0)
  // Set when a command is picked so the autoResize re-check can't bounce
  // showCommands back to true before the user edits the text again.
  let commandJustPicked = false

  function selectCommand(name: string) {
    const next = '/' + name + ' '
    text = next
    showCommands = false
    selectedCommandIdx = 0
    commandFilter = ''
    commandJustPicked = true
    // Restore focus + place caret at the end on the next tick so the
    // programmatic value update has landed in the DOM.
    setTimeout(() => {
      if (!textarea) return
      textarea.focus()
      textarea.setSelectionRange(next.length, next.length)
      autoResize()
    }, 0)
  }

  const builtinCommands = [
    { name: 'new', desc: 'Start a new conversation' },
    { name: 'skills', desc: 'List available skills' },
  ]

  // Tiers marked force_command are exposed as /<tier> autocompletions regardless
  // of their enabled state — force_command bypasses enabled (same rule as backend).
  let allCommands = $derived.by(() => {
    const cmds = [...builtinCommands]
    for (const t of tiers) {
      if (!t.force_command) continue
      cmds.push({ name: t.name, desc: `Force tier: ${t.model}` })
    }
    return cmds
  })

  let filteredCommands = $derived.by(() => {
    if (!commandFilter) return allCommands
    const q = commandFilter.toLowerCase()
    return allCommands.filter(c => c.name.toLowerCase().includes(q))
  })

  function autoResize() {
    if (!textarea) return
    textarea.style.height = 'auto'
    textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px'

    // Detect slash command input
    const val = textarea.value
    if (commandJustPicked) {
      // A selection just populated the textarea programmatically; ignore
      // this pass so the dropdown stays closed until the user types again.
      commandJustPicked = false
      showCommands = false
      return
    }
    if (val.startsWith('/') && !val.includes(' ')) {
      commandFilter = val.slice(1)
      showCommands = true
      selectedCommandIdx = 0
    } else {
      showCommands = false
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (showCommands && filteredCommands.length > 0) {
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        selectedCommandIdx = (selectedCommandIdx + 1) % filteredCommands.length
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        selectedCommandIdx = (selectedCommandIdx - 1 + filteredCommands.length) % filteredCommands.length
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey)) {
        e.preventDefault()
        selectCommand(filteredCommands[selectedCommandIdx].name)
        return
      }
      if (e.key === 'Escape') {
        showCommands = false
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  function send() {
    const msg = text.trim()
    if (!msg && files.length === 0) return

    onSend(msg, [...files], selectedModel)
    text = ''
    for (const f of files) {
      if (f.previewUrl) URL.revokeObjectURL(f.previewUrl)
    }
    files = []
    if (textarea) {
      textarea.style.height = 'auto'
    }
  }

  async function uploadFile(file: File) {
    uploading = true
    try {
      const form = new FormData()
      form.append('file', file)

      // Determine media type
      let mediaType = 'document'
      if (file.type.startsWith('image/')) mediaType = 'photo'
      else if (file.type.startsWith('video/')) mediaType = 'video'
      else if (file.type.startsWith('audio/')) mediaType = 'voice'
      form.append('type', mediaType)

      const res = await fetch('/api/chat/upload', {
        method: 'POST',
        body: form,
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
      })
      if (!res.ok) throw new Error('Upload failed')

      const data = await res.json()
      const uploaded: UploadedFile = {
        upload_id: data.upload_id,
        file_name: data.file_name,
        mime_type: data.mime_type,
      }
      if (file.type.startsWith('image/')) {
        uploaded.previewUrl = URL.createObjectURL(file)
      }
      files = [...files, uploaded]
    } catch (e: any) {
      toasts.show(e.message || 'Upload failed', 'error')
    } finally {
      uploading = false
    }
  }

  function handleFileSelect(e: Event) {
    const input = e.target as HTMLInputElement
    if (input.files) {
      for (const f of input.files) uploadFile(f)
    }
    input.value = ''
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault()
    dragOver = false
    if (e.dataTransfer?.files) {
      for (const f of e.dataTransfer.files) uploadFile(f)
    }
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault()
    dragOver = true
  }

  function handlePaste(e: ClipboardEvent) {
    const items = e.clipboardData?.items
    if (!items) return
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        e.preventDefault()
        const file = item.getAsFile()
        if (file) uploadFile(file)
      }
    }
  }

  function removeFile(idx: number) {
    const removed = files[idx]
    if (removed?.previewUrl) URL.revokeObjectURL(removed.previewUrl)
    files = files.filter((_, i) => i !== idx)
  }
</script>

<div
  class="chat-input-container"
  class:drag-over={dragOver}
  ondrop={handleDrop}
  ondragover={handleDragOver}
  ondragleave={() => dragOver = false}
  role="form"
>
  <!-- File chips -->
  {#if files.length > 0}
    <div class="file-chips">
      {#each files as file, i}
        <div class="file-chip">
          {#if file.previewUrl}
            <img src={file.previewUrl} alt={file.file_name} class="file-chip-thumb" />
          {/if}
          <span class="file-chip-name">{file.file_name}</span>
          <button class="file-chip-remove" onclick={() => removeFile(i)}><X size={12} /></button>
        </div>
      {/each}
    </div>
  {/if}

  <!-- Active skills pills -->
  {#if activeSkills.length > 0}
    <div class="skill-pills">
      <Sparkles size={12} class="skill-icon" />
      {#each activeSkills as skill}
        <span class="skill-pill">
          {skill}
          <button class="skill-pill-remove" onclick={() => onDismissSkill?.(skill)} title="Dismiss {skill}">
            <X size={10} />
          </button>
        </span>
      {/each}
    </div>
  {/if}

  <!-- Slash command suggestions -->
  {#if showCommands && filteredCommands.length > 0}
    <div class="command-list">
      {#each filteredCommands as cmd, i}
        <button
          class="command-item"
          type="button"
          class:selected={i === selectedCommandIdx}
          onmousedown={(e) => { e.preventDefault(); selectCommand(cmd.name) }}
          onmouseenter={() => selectedCommandIdx = i}
        >
          <span class="command-name">/{cmd.name}</span>
          <span class="command-desc">{cmd.desc}</span>
        </button>
      {/each}
    </div>
  {/if}

  <div class="input-row">
    <!-- Attach button -->
    <button class="attach-btn" onclick={() => fileInput.click()} disabled={uploading}>
      <Paperclip size={16} />
    </button>
    <input
      type="file"
      bind:this={fileInput}
      onchange={handleFileSelect}
      multiple
      hidden
    />

    <!-- Textarea -->
    <textarea
      bind:this={textarea}
      bind:value={text}
      placeholder={sending ? 'Type to queue...' : 'Type a message...'}
      class="chat-textarea"
      rows="1"
      oninput={() => { autoResize(); onInput(); }}
      onkeydown={handleKeydown}
      onpaste={handlePaste}
    ></textarea>

    <!-- Tier selector (custom upward dropdown) -->
    {#if tiers.length > 0}
      <div class="tier-dropdown-wrapper">
        <button
          class="tier-select"
          bind:this={tierBtnEl}
          onclick={() => tierDropdownOpen = !tierDropdownOpen}
          type="button"
        >
          {selectedModel || 'Auto'} <span class="tier-caret">&#9650;</span>
        </button>
        {#if tierDropdownOpen}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div class="tier-dropdown">
            <button class="tier-option" class:active={!selectedModel} onclick={() => selectTier('')}>Auto</button>
            {#each tiers as tier}
              <button class="tier-option" class:active={selectedModel === tier.name} onclick={() => selectTier(tier.name)}>{tier.name}</button>
            {/each}
          </div>
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div class="tier-backdrop" onclick={() => tierDropdownOpen = false}></div>
        {/if}
      </div>
    {/if}

    <!-- Send / Stop button -->
    {#if sending}
      <button class="stop-btn" onclick={() => onStop?.()} title="Stop generation">
        <Square size={16} />
      </button>
    {:else}
      <button class="send-btn" onclick={send} disabled={!text.trim() && files.length === 0}>
        <Send size={16} />
      </button>
    {/if}
  </div>

  {#if uploading}
    <div class="upload-indicator">Uploading...</div>
  {/if}
</div>

<style>
  .chat-input-container {
    border-top: 1px solid var(--border);
    padding: 8px 12px;
    padding-bottom: calc(8px + env(safe-area-inset-bottom, 0px));
    background: var(--bg);
    transition: background 0.15s;
  }

  .chat-input-container.drag-over {
    background: var(--bg-input);
    outline: 2px dashed var(--accent);
    outline-offset: -2px;
  }

  .input-row {
    display: flex;
    align-items: flex-end;
    gap: 8px;
  }

  .chat-textarea {
    flex: 1;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: var(--alf-chat-font-size, var(--font-sm, 13px));
    resize: none;
    overflow-y: auto;
    line-height: 1.5;
    min-height: 38px;
    max-height: 200px;
  }

  .chat-textarea:focus {
    outline: none;
    border-color: var(--accent);
  }

  .chat-textarea:disabled {
    opacity: 0.5;
  }

  .attach-btn, .send-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    border: none;
    border-radius: var(--radius, 8px);
    cursor: pointer;
    transition: background 0.15s;
    flex-shrink: 0;
  }

  .attach-btn {
    background: var(--bg-input);
    color: var(--text-dim);
  }

  .attach-btn:hover {
    background: var(--border);
    color: var(--text);
  }

  .send-btn {
    background: var(--accent);
    color: var(--on-accent);
  }

  .stop-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    border: none;
    border-radius: var(--radius, 8px);
    cursor: pointer;
    flex-shrink: 0;
    background: var(--red, #e53935);
    color: #fff;
    transition: opacity 0.15s;
  }

  .stop-btn:hover { opacity: 0.85; }

  .send-btn:hover:not(:disabled) {
    opacity: 0.9;
  }

  .send-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  .tier-dropdown-wrapper {
    position: relative;
    flex-shrink: 0;
  }

  .tier-select {
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-xs, 11px);
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .tier-caret {
    font-size: 8px;
    opacity: 0.5;
  }

  .tier-backdrop {
    position: fixed;
    inset: 0;
    z-index: 99;
  }

  .tier-dropdown {
    position: absolute;
    bottom: 100%;
    right: 0;
    margin-bottom: 4px;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    box-shadow: 0 -4px 16px rgba(0, 0, 0, 0.15);
    z-index: 100;
    min-width: 120px;
    max-height: 240px;
    overflow-y: auto;
    padding: 4px;
  }

  .tier-option {
    display: block;
    width: 100%;
    padding: 6px 10px;
    border: none;
    background: transparent;
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-xs, 11px);
    text-align: left;
    cursor: pointer;
    border-radius: 4px;
    white-space: nowrap;
  }

  .tier-option:hover {
    background: var(--bg-input);
  }

  .tier-option.active {
    color: var(--accent);
    font-weight: 600;
  }

  /* File chips */
  .file-chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 8px;
  }

  .file-chip {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 6px;
    font-size: var(--font-xs, 11px);
  }

  .file-chip-thumb {
    width: 28px;
    height: 28px;
    object-fit: cover;
    border-radius: 4px;
  }

  .file-chip-name {
    max-width: 150px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .file-chip-remove {
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    padding: 0;
    display: flex;
    align-items: center;
  }

  .upload-indicator {
    font-size: var(--font-xs, 11px);
    color: var(--text-dim);
    padding: 4px 0 0;
  }

  /* Active skills */
  .skill-pills {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 8px;
  }

  .skill-pills :global(.skill-icon) {
    color: var(--accent);
    flex-shrink: 0;
  }

  .skill-pill {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 2px 8px 2px 10px;
    background: color-mix(in srgb, var(--accent) 10%, transparent);
    border: 1px solid color-mix(in srgb, var(--accent) 25%, transparent);
    border-radius: 12px;
    font-size: var(--font-xs, 11px);
    font-weight: 500;
    color: var(--accent);
  }

  .skill-pill-remove {
    background: none;
    border: none;
    color: var(--accent);
    cursor: pointer;
    padding: 0;
    display: flex;
    align-items: center;
    opacity: 0.6;
    transition: opacity 0.15s;
  }

  .skill-pill-remove:hover {
    opacity: 1;
  }

  /* Slash command suggestions */
  .command-list {
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 8px;
    margin-bottom: 6px;
    max-height: 200px;
    overflow-y: auto;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  }

  .command-item {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 8px 12px;
    background: none;
    border: none;
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    text-align: left;
  }

  .command-item:hover,
  .command-item.selected {
    background: var(--bg-input);
  }

  .command-name {
    font-weight: 600;
    font-family: 'JetBrains Mono', monospace;
    font-size: var(--font-sm, 13px);
    min-width: 80px;
  }

  .command-desc {
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
  }
</style>
