<script lang="ts">
  import { Paperclip, Send, X } from 'lucide-svelte'
  import { api } from '../../lib/api'
  import { toasts } from '../../stores/toast.svelte'

  interface UploadedFile {
    upload_id: string
    file_name: string
    mime_type: string
  }

  interface Props {
    onSend: (message: string, mediaIds: string[], model: string) => void
    sending: boolean
    tiers?: { name: string; model: string }[]
  }

  let { onSend, sending, tiers = [] }: Props = $props()

  let text = $state('')
  let files = $state<UploadedFile[]>([])
  let uploading = $state(false)
  let selectedModel = $state('')
  let textarea: HTMLTextAreaElement
  let fileInput: HTMLInputElement
  let dragOver = $state(false)

  function autoResize() {
    if (!textarea) return
    textarea.style.height = 'auto'
    textarea.style.height = Math.min(textarea.scrollHeight, 200) + 'px'
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      send()
    }
  }

  function send() {
    const msg = text.trim()
    if (!msg && files.length === 0) return
    if (sending) return

    const mediaIds = files.map(f => f.upload_id)
    onSend(msg, mediaIds, selectedModel)
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
      placeholder={sending ? 'Sending...' : 'Type a message...'}
      class="chat-textarea"
      rows="1"
      disabled={sending}
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

    <!-- Send button -->
    <button class="send-btn" onclick={send} disabled={sending || (!text.trim() && files.length === 0)}>
      <Send size={16} />
    </button>
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
</style>
