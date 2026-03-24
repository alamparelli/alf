<script lang="ts">
  import { marked } from 'marked'
  import DOMPurify from 'dompurify'
  import { ChevronDown, ChevronRight, Wrench, Brain, SmilePlus, Image } from 'lucide-svelte'
  import { api } from '../../lib/api'
  import { toasts } from '../../stores/toast.svelte'

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
      media?: MediaRef[]
      reactions?: Reaction[]
      content_blocks?: ContentBlock[]
    }
    convId: string
  }

  let { msg, convId }: Props = $props()

  let showEmojiPicker = $state(false)
  const quickEmojis = ['👍', '❤️', '😂', '🎯', '🔥', '💡', '✅', '❌', '🤔', '👀']

  // Collapsible blocks
  let expandedBlocks = $state<Record<number, boolean>>({})

  function toggleBlock(idx: number) {
    expandedBlocks[idx] = !expandedBlocks[idx]
  }

  function renderMarkdown(text: string): string {
    if (!text) return ''
    const raw = marked.parse(text, { async: false }) as string
    return DOMPurify.sanitize(raw)
  }

  async function react(emoji: string) {
    showEmojiPicker = false
    try {
      await api('/api/chat/react', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ msg_id: msg.id, emoji })
      })
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

<div class="chat-msg chat-msg-{msg.role}">
  <!-- Media attachments (user) -->
  {#if msg.media && msg.media.length > 0}
    <div class="msg-media">
      {#each msg.media as m}
        {#if isImageMime(m.mime_type)}
          <img
            src={m.url || `/api/chat/media/${m.upload_id}`}
            alt={m.file_name}
            class="msg-media-img"
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
        <div class="content-block tool-result-block">
          <div class="block-body tool-result-body">
            <pre>{block.content || block.text || ''}</pre>
          </div>
        </div>
      {:else if block.type === 'text'}
        <div class="msg-text">{@html renderMarkdown(block.text || '')}</div>
      {/if}
    {/each}
  {:else}
    <!-- Plain text message -->
    {#if msg.role === 'assistant'}
      <div class="msg-text">{@html renderMarkdown(msg.text)}</div>
    {:else}
      <div class="msg-text">{msg.text}</div>
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

    <!-- Reactions -->
    {#if msg.reactions && msg.reactions.length > 0}
      <div class="msg-reactions">
        {#each msg.reactions as r}
          <span class="reaction-chip" class:reaction-alf={r.from === 'alf'}>{r.emoji}</span>
        {/each}
      </div>
    {/if}

    <!-- React button (assistant messages only) -->
    {#if msg.role === 'assistant'}
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
    {/if}
  </div>
</div>

<style>
  .chat-msg {
    max-width: 85%;
    padding: 10px 14px;
    border-radius: 12px;
    margin-bottom: 8px;
    position: relative;
    word-wrap: break-word;
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
    font-size: 0.8rem;
    font-style: italic;
    max-width: 90%;
    text-align: center;
  }

  /* Markdown content */
  .msg-text {
    font-size: 0.88rem;
    line-height: 1.6;
  }

  .msg-text :global(pre) {
    background: rgba(0, 0, 0, 0.15);
    padding: 8px 12px;
    border-radius: 6px;
    overflow-x: auto;
    font-size: 0.8rem;
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
    background: rgba(0, 0, 0, 0.1);
    border: none;
    color: inherit;
    font-family: inherit;
    font-size: 0.78rem;
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
    background: rgba(0, 0, 0, 0.08);
    font-size: 0.78rem;
  }

  .block-body pre {
    white-space: pre-wrap;
    word-break: break-word;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.76rem;
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

  .msg-media-file {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    background: rgba(0, 0, 0, 0.1);
    border-radius: 4px;
    font-size: 0.78rem;
  }

  /* Footer */
  .msg-footer {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 4px;
    flex-wrap: wrap;
  }

  .msg-time {
    font-size: 0.68rem;
    opacity: 0.5;
  }

  .msg-model, .msg-cost {
    font-size: 0.65rem;
    opacity: 0.5;
    font-family: 'JetBrains Mono', monospace;
  }

  /* Reactions */
  .msg-reactions {
    display: flex;
    gap: 4px;
  }

  .reaction-chip {
    font-size: 0.85rem;
    padding: 1px 4px;
    border-radius: 4px;
    background: rgba(0, 0, 0, 0.08);
  }

  .reaction-alf {
    background: rgba(0, 0, 0, 0.15);
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
    z-index: 10;
  }

  .emoji-btn {
    background: none;
    border: none;
    cursor: pointer;
    font-size: 1.1rem;
    padding: 4px;
    border-radius: 4px;
    transition: background 0.1s;
  }

  .emoji-btn:hover {
    background: var(--bg-input);
  }
</style>
