<script lang="ts">
  import { Paperclip, Send, Square, X } from 'lucide-svelte'
  import { api } from '../../lib/api'
  import { toasts } from '../../stores/toast.svelte'

  interface UploadedFile {
    upload_id: string
    file_name: string
    mime_type: string
  }

  interface Props {
    onSend: (message: string, mediaFiles: UploadedFile[], model: string) => void
    onStop?: () => void
    sending: boolean
    tiers?: { name: string; model: string }[]
  }

  let { onSend, onStop, sending, tiers = [] }: Props = $props()

  let text = $state('')
  let files = $state<UploadedFile[]>([])
  let uploading = $state(false)
  let selectedModel = $state('')
  let textarea: HTMLTextAreaElement
  let fileInput: HTMLInputElement
  let dragOver = $state(false)

  // Slash commands
  let showCommands = $state(false)
  let commandFilter = $state('')
  let selectedCommandIdx = $state(0)

  const builtinCommands = [
    { name: 'new', desc: 'Start a new conversation' },
    { name: 'clear', desc: 'Clear conversation (alias for /new)' },
    { name: 'skills', desc: 'List available skills' },
  ]

  // Add tier force commands
  let allCommands = $derived.by(() => {
    const cmds = [...builtinCommands]
    for (const t of tiers) {
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
        const cmd = filteredCommands[selectedCommandIdx]
        text = '/' + cmd.name + ' '
        showCommands = false
        autoResize()
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
      files = [...files, {
        upload_id: data.upload_id,
        file_name: data.file_name,
        mime_type: data.mime_type,
      }]
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

  function removeFile(idx: number) {
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
          <span class="file-chip-name">{file.file_name}</span>
          <button class="file-chip-remove" onclick={() => removeFile(i)}><X size={12} /></button>
        </div>
      {/each}
    </div>
  {/if}

  <!-- Slash command suggestions -->
  {#if showCommands && filteredCommands.length > 0}
    <div class="command-list">
      {#each filteredCommands as cmd, i}
        <button
          class="command-item"
          class:selected={i === selectedCommandIdx}
          onclick={() => { text = '/' + cmd.name + ' '; showCommands = false; textarea?.focus() }}
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
      oninput={autoResize}
      onkeydown={handleKeydown}
    ></textarea>

    <!-- Tier selector -->
    {#if tiers.length > 0}
      <select class="tier-select" bind:value={selectedModel}>
        <option value="">Auto</option>
        {#each tiers as tier}
          <option value={tier.name}>{tier.name}</option>
        {/each}
      </select>
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
    font-size: 0.88rem;
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

  .tier-select {
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.75rem;
    cursor: pointer;
    flex-shrink: 0;
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
    font-size: 0.75rem;
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
    font-size: 0.72rem;
    color: var(--text-dim);
    padding: 4px 0 0;
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
    font-size: 0.82rem;
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
    font-size: 0.8rem;
    min-width: 80px;
  }

  .command-desc {
    color: var(--text-dim);
    font-size: 0.78rem;
  }
</style>
