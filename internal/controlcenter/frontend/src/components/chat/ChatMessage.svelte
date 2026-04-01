<script lang="ts">
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'
  import { ChevronDown, ChevronRight, Wrench, Brain, SmilePlus, Image, Clipboard, Check, Users } from 'lucide-svelte'
  import { api } from '../../lib/api'
  import { isStandaloneEmojiText } from '../../lib/chat'
  import { toasts } from '../../stores/toast.svelte'
  import { nav } from '../../stores/nav.svelte'

  interface ContentBlock {
    type: string
    text?: string
    name?: string
    input?: any
    content?: string
    thinking?: string
  }

  interface MediaRef {
    upload_id: string
    type: string
    file_name: string
    mime_type: string
    url?: string
  }

  interface Reaction {
    emoji: string
    from: string
  }

  interface Props {
    msg: {
      id: string
      role: string
      text: string
      ts: string
      model?: string
      tier?: string
      cost_usd?: number
      duration_ms?: number
      skills?: string[]
      media?: MediaRef[]
      reactions?: Reaction[]
      content_blocks?: ContentBlock[]
    }
    convId: string
    onSendToTask?: (text: string) => void
  }

  let { msg, convId, onSendToTask }: Props = $props()

  let showEmojiPicker = $state(false)
  let copied = $state(false)
  let lightboxSrc = $state('')
  const quickEmojis = ['👍', '❤️', '😂', '🎯', '🔥', '💡', '✅', '❌', '🤔', '👀']

  async function copyText() {
    const blocks = msg.content_blocks
    let text = ''
    if (blocks && blocks.length > 0) {
      text = blocks.filter(b => b.type === 'text').map(b => b.text || '').join('\n')
    }
    if (!text) text = msg.text || ''
    try {
      await navigator.clipboard.writeText(text)
      copied = true
      setTimeout(() => { copied = false }, 1500)
    } catch {
      toasts.show('Failed to copy', 'error')
    }
  }

  // Collapsible blocks
  let expandedBlocks = $state<Record<number, boolean>>({})

  function toggleBlock(idx: number) {
    expandedBlocks[idx] = !expandedBlocks[idx]
  }

  function fixPipeTables(text: string): string {
    // Fix markdown tables output on a single line (common with Codex models).
    // Detects patterns like "| H1 | H2 | |---|---| | a | b |" and adds newlines.
    return text.replace(
      /(\|[^|\n]+(?:\|[^|\n]+)+\|)\s*(\|[-:\s|]+\|)\s*((?:\|[^|\n]+(?:\|[^|\n]+)+\|\s*)+)/g,
      (_, header, separator, rows) => {
        const fixedRows = rows.trim().replace(/\|\s*\|/g, '|\n|').replace(/\|\s*$/gm, '|')
        return `${header.trim()}\n${separator.trim()}\n${fixedRows}`
      }
    )
  }

  function renderMarkdown(text: string): string {
    if (!text) return ''
    // Fix single-line pipe tables before markdown parsing.
    const withTables = fixPipeTables(text)
    // Auto-convert bare image/gif/video URLs (on their own line) to markdown images
    // so they render inline instead of as plain links.
    const withMedia = withTables.replace(
      /^(https?:\/\/\S+\.(?:gif|png|jpe?g|webp|svg)(?:\?[^\s]*)?)$/gim,
      '![]($1)'
    )
    const raw = marked.parse(withMedia, { async: false }) as string
    // Convert <img> with video extensions to <video> elements.
    const withVideos = raw.replace(
      /<img\s+src="([^"]+\.(?:mp4|webm|mov)(?:\?[^"]*)?)"\s*(?:alt="([^"]*)")?\s*\/?>/gi,
      '<video src="$1" controls playsinline class="chat-video">$2</video>'
    )
    return DOMPurify.sanitize(withVideos, {
      ADD_TAGS: ['video'],
      ADD_ATTR: ['controls', 'playsinline', 'autoplay', 'loop', 'muted'],
      ALLOWED_URI_REGEXP: /^(?:(?:(?:f|ht)tps?|mailto|tel|callto|sms|cid|xmpp|alf):|[^a-z]|[a-z+.\-]+(?:[^a-z+.\-:]|$))/i,
    })
  }

  function handleLinkClick(e: MouseEvent) {
    const target = (e.target as HTMLElement).closest('a')
    if (!target?.href) return

    let url: URL
    try { url = new URL(target.href) } catch { return }
    if (url.protocol !== 'alf:') return

    e.preventDefault()
    const type = url.hostname          // 'files', 'dirs', 'apps', 'view'
    const path = url.pathname.slice(1) // strip leading /

    switch (type) {
      case 'files':
        nav.navigateTo('home')
        setTimeout(() => window.dispatchEvent(
          new CustomEvent('alf:open-file', { detail: { path } })
        ), 100)
        break
      case 'dirs':
        nav.navigateTo('home')
        setTimeout(() => window.dispatchEvent(
          new CustomEvent('alf:open-dir', { detail: { path } })
        ), 100)
        break
      case 'apps':
        nav.navigateTo(`page:${path}`)
        break
      case 'view':
        nav.navigateTo(path)
        break
    }
  }

  function getFullText(): string {
    const blocks = msg.content_blocks
    if (blocks && blocks.length > 0) {
      return blocks.filter(b => b.type === 'text').map(b => b.text || '').join('\n')
    }
    return msg.text || ''
  }

  function getStandaloneEmojiCandidate(): string {
    if (msg.media && msg.media.length > 0) return ''

    const blocks = msg.content_blocks
    if (blocks && blocks.length > 0) {
      if (blocks.some(block => block.type !== 'text')) return ''
      return blocks.map(block => block.text || '').join('')
    }

    return msg.text || ''
  }

  function isStandaloneEmojiMessage(): boolean {
    return isStandaloneEmojiText(getStandaloneEmojiCandidate())
  }

  function handleSendToTask() {
    const text = getFullText()
    if (text.length > 10 && onSendToTask) {
      onSendToTask(text.substring(0, 2000))
    }
  }

  async function react(emoji: string) {
    showEmojiPicker = false
    try {
      const result = await api('POST', '/api/chat/react', { msg_id: msg.id, emoji })
      // Optimistically add user reaction locally
      const newReaction: Reaction = { emoji, from: 'user' }
      if (msg.reactions) {
        msg.reactions = [...msg.reactions, newReaction]
      } else {
        msg.reactions = [newReaction]
      }
      // Add ALF's mirror reaction if returned
      if (result.mirror) {
        msg.reactions = [...msg.reactions, { emoji: result.mirror, from: 'alf' }]
      }
    } catch (e: any) {
      toasts.show(e.error || 'Failed to react', 'error')
    }
  }


  function formatTime(ts: string): string {
    try {
      return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    } catch {
      return ''
    }
  }

  function isImageMime(mime: string): boolean {
    return mime?.startsWith('image/')
  }
</script>

{#if showEmojiPicker}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div class="emoji-backdrop" onclick={() => showEmojiPicker = false}></div>
{/if}

<div class="chat-msg chat-msg-{msg.role}" class:standalone-emoji={isStandaloneEmojiMessage()}>
  <!-- Media attachments (user) -->
  {#if msg.media && msg.media.length > 0}
    <div class="msg-media">
      {#each msg.media as m}
        {#if isImageMime(m.mime_type)}
          <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_noninteractive_element_interactions -->
          <img
            src={m.url || `/api/chat/media/${m.upload_id}`}
            alt={m.file_name}
            class="msg-media-img"
            onclick={() => lightboxSrc = m.url || `/api/chat/media/${m.upload_id}`}
          />
        {:else}
          <div class="msg-media-file">
            <Image size={14} />
            <span>{m.file_name}</span>
          </div>
        {/if}
      {/each}
    </div>
  {/if}

  <!-- Content blocks (assistant streaming) -->
  {#if msg.content_blocks && msg.content_blocks.length > 0}
    {#each msg.content_blocks as block, i}
      {#if block.type === 'thinking'}
        <div class="content-block thinking-block">
          <button class="block-header" onclick={() => toggleBlock(i)}>
            {#if expandedBlocks[i]}
              <ChevronDown size={14} />
            {:else}
              <ChevronRight size={14} />
            {/if}
            <Brain size={14} />
            <span>Thinking</span>
          </button>
          {#if expandedBlocks[i]}
            <div class="block-body thinking-body">
              <pre>{block.thinking || block.text || ''}</pre>
            </div>
          {/if}
        </div>
      {:else if block.type === 'tool_use'}
        <div class="content-block tool-block">
          <button class="block-header" onclick={() => toggleBlock(i)}>
            {#if expandedBlocks[i]}
              <ChevronDown size={14} />
            {:else}
              <ChevronRight size={14} />
            {/if}
            <Wrench size={14} />
            <span>{block.name || 'Tool'}</span>
          </button>
          {#if expandedBlocks[i]}
            <div class="block-body tool-body">
              <pre>{typeof block.input === 'string' ? block.input : JSON.stringify(block.input, null, 2)}</pre>
            </div>
          {/if}
        </div>
      {:else if block.type === 'tool_result'}
        {@const resultText = (block.content || block.text || '').trim()}
        {#if resultText.length > 2}
          <div class="content-block tool-result-block">
            <div class="block-body tool-result-body">
              <pre>{resultText}</pre>
            </div>
          </div>
        {/if}
      {:else if block.type === 'text'}
        <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
        <div class="msg-text" class:standalone-emoji-text={isStandaloneEmojiMessage()} onclick={handleLinkClick}>{@html renderMarkdown(block.text || '')}</div>
      {/if}
    {/each}
  {:else}
    <!-- Plain text message -->
    {#if msg.role === 'assistant' || msg.role === 'system'}
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="msg-text" class:standalone-emoji-text={isStandaloneEmojiMessage()} onclick={handleLinkClick}>{@html renderMarkdown(msg.text)}</div>
    {:else}
      <div class="msg-text" class:standalone-emoji-text={isStandaloneEmojiMessage()}>{msg.text}</div>
    {/if}
  {/if}

  <!-- Footer: time, model, reactions -->
  <div class="msg-footer">
    <span class="msg-time">{formatTime(msg.ts)}</span>
    {#if msg.model}
      <span class="msg-model">{msg.tier || msg.model}</span>
    {/if}
    {#if msg.cost_usd && msg.cost_usd > 0}
      <span class="msg-cost">${msg.cost_usd.toFixed(4)}</span>
    {/if}
    {#if msg.duration_ms}
      <span class="msg-duration">{(msg.duration_ms / 1000).toFixed(1)}s</span>
    {/if}
    {#if msg.skills && msg.skills.length > 0}
      <span class="msg-skills">{msg.skills.join(', ')}</span>
    {/if}

    <!-- Reactions -->
    {#if msg.reactions && msg.reactions.length > 0}
      <div class="msg-reactions">
        {#each msg.reactions as r}
          <span class="reaction-chip" class:reaction-alf={r.from === 'alf'}>{r.emoji}</span>
        {/each}
      </div>
    {/if}

    <!-- Copy + React buttons (assistant messages only) -->
    {#if msg.role === 'assistant'}
      <button class="copy-btn" onclick={copyText} title="Copy message">
        {#if copied}
          <Check size={13} />
        {:else}
          <Clipboard size={13} />
        {/if}
      </button>
      <div class="react-container">
        <button class="react-btn" onclick={() => showEmojiPicker = !showEmojiPicker}>
          <SmilePlus size={13} />
        </button>
        {#if showEmojiPicker}
          <div class="emoji-picker">
            {#each quickEmojis as emoji}
              <button class="emoji-btn" onclick={() => react(emoji)}>{emoji}</button>
            {/each}
          </div>
        {/if}
      </div>
      {#if onSendToTask && getFullText().length > 10}
        <button class="copy-btn" onclick={handleSendToTask} title="Send to agents">
          <Users size={13} />
        </button>
      {/if}
    {/if}
  </div>
</div>

<!-- Lightbox -->
{#if lightboxSrc}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="lightbox" onclick={() => lightboxSrc = ''}>
    <img src={lightboxSrc} alt="Full size" />
  </div>
{/if}

<style>
  .chat-msg {
    max-width: 85%;
    padding: 10px 14px;
    border-radius: 12px;
    margin-bottom: 8px;
    position: relative;
    word-wrap: break-word;
    overflow: visible;
  }

  .chat-msg-user {
    align-self: flex-end;
    background: var(--accent);
    color: var(--on-accent);
    border-bottom-right-radius: 4px;
  }

  .chat-msg-assistant {
    align-self: flex-start;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-bottom-left-radius: 4px;
  }

  .chat-msg-system {
    align-self: center;
    background: var(--bg-input);
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
    font-style: italic;
    max-width: 90%;
    text-align: center;
  }

  /* Markdown content */
  .msg-text {
    font-size: var(--font-sm, 13px);
    line-height: 1.6;
  }

  .chat-msg.standalone-emoji {
    padding: 8px 12px;
  }

  .msg-text.standalone-emoji-text {
    font-size: clamp(2.5rem, 7vw, 4rem);
    line-height: 1.05;
  }

  .msg-text.standalone-emoji-text :global(p) {
    margin: 0;
  }

  .chat-msg-user .msg-text {
    white-space: pre-wrap;
  }

  .msg-text :global(pre) {
    background: color-mix(in srgb, var(--text) 15%, transparent);
    padding: 8px 12px;
    border-radius: 6px;
    overflow-x: auto;
    font-size: var(--font-sm, 13px);
    margin: 8px 0;
  }

  .msg-text :global(code) {
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.82em;
  }

  .msg-text :global(p) {
    margin: 4px 0;
  }

  .msg-text :global(ul), .msg-text :global(ol) {
    margin: 4px 0;
    padding-left: 20px;
  }

  .msg-text :global(a) {
    color: inherit;
    text-decoration: underline;
  }

  .msg-text :global(table) {
    border-collapse: collapse;
    width: 100%;
    margin: 8px 0;
    font-size: var(--font-sm, 13px);
  }

  .msg-text :global(th),
  .msg-text :global(td) {
    border: 1px solid color-mix(in srgb, var(--text) 20%, transparent);
    padding: 6px 10px;
    text-align: left;
  }

  .msg-text :global(th) {
    font-weight: 600;
    background: color-mix(in srgb, var(--text) 8%, transparent);
  }

  .msg-text :global(tr:hover) {
    background: color-mix(in srgb, var(--text) 5%, transparent);
  }

  .msg-text :global(img) {
    max-width: 300px;
    max-height: 200px;
    border-radius: 8px;
    margin: 4px 0;
    display: block;
  }

  .msg-text :global(video) {
    max-width: 400px;
    max-height: 300px;
    border-radius: 8px;
    margin: 4px 0;
    display: block;
  }

  /* Content blocks */
  .content-block {
    margin: 6px 0;
    border-radius: 6px;
    overflow: hidden;
  }

  .block-header {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 6px 10px;
    background: color-mix(in srgb, var(--text) 10%, transparent);
    border: none;
    color: inherit;
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    font-weight: 500;
    cursor: pointer;
    text-align: left;
    opacity: 0.8;
  }

  .block-header:hover {
    opacity: 1;
  }

  .block-body {
    padding: 8px 10px;
    background: color-mix(in srgb, var(--text) 8%, transparent);
    font-size: var(--font-sm, 13px);
  }

  .block-body pre {
    white-space: pre-wrap;
    word-break: break-word;
    font-family: 'JetBrains Mono', monospace;
    font-size: var(--font-xs, 11px);
    margin: 0;
  }

  .thinking-body {
    color: var(--text-dim);
    font-style: italic;
  }

  .tool-result-block {
    border-left: 2px solid var(--text-dim);
    margin-left: 8px;
  }

  .tool-result-body {
    max-height: 200px;
    overflow-y: auto;
  }

  /* Media */
  .msg-media {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 6px;
  }

  .msg-media-img {
    max-width: 240px;
    max-height: 180px;
    border-radius: 8px;
    cursor: pointer;
    object-fit: cover;
  }

  .lightbox {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, #000 85%, transparent);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 2000;
    cursor: zoom-out;
  }

  .lightbox img {
    max-width: 90vw;
    max-height: 90vh;
    border-radius: 8px;
    object-fit: contain;
  }

  .msg-media-file {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    background: color-mix(in srgb, var(--text) 10%, transparent);
    border-radius: 4px;
    font-size: var(--font-sm, 13px);
  }

  /* Footer */
  .msg-footer {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 4px;
    flex-wrap: wrap;
  }

  .chat-msg.standalone-emoji .msg-footer {
    margin-top: 6px;
  }

  .msg-time {
    font-size: var(--font-xs, 11px);
    opacity: 0.5;
  }

  .msg-model, .msg-cost, .msg-duration, .msg-skills {
    font-size: var(--font-xs, 11px);
    opacity: 0.5;
    font-family: 'JetBrains Mono', monospace;
  }

  /* Reactions */
  .msg-reactions {
    display: flex;
    gap: 4px;
  }

  .reaction-chip {
    font-size: var(--font-sm, 13px);
    padding: 1px 4px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--text) 8%, transparent);
  }

  .reaction-alf {
    background: color-mix(in srgb, var(--text) 15%, transparent);
  }

  /* Copy button */
  .copy-btn {
    background: none;
    border: none;
    color: inherit;
    opacity: 0.3;
    cursor: pointer;
    padding: 2px;
    display: flex;
    align-items: center;
    transition: opacity 0.15s;
  }

  .copy-btn:hover {
    opacity: 0.8;
  }

  /* React button */
  .react-container {
    position: relative;
  }

  .react-btn {
    background: none;
    border: none;
    color: inherit;
    opacity: 0.3;
    cursor: pointer;
    padding: 2px;
    display: flex;
    align-items: center;
    transition: opacity 0.15s;
  }

  .react-btn:hover {
    opacity: 0.8;
  }

  .emoji-backdrop {
    position: fixed;
    inset: 0;
    z-index: 99;
  }

  .emoji-picker {
    position: absolute;
    bottom: 100%;
    right: 0;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 6px;
    display: flex;
    flex-wrap: wrap;
    gap: 2px;
    width: 200px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
    z-index: 100;
    margin-bottom: 4px;
  }

  .emoji-btn {
    background: none;
    border: none;
    cursor: pointer;
    font-size: var(--font-lg, 18px);
    padding: 4px;
    border-radius: 4px;
    transition: background 0.1s;
  }

  .emoji-btn:hover {
    background: var(--bg-input);
  }

  @media (max-width: 768px) {
    .chat-msg {
      max-width: 90%;
    }
  }
</style>
