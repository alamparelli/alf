<script lang="ts">
  import { X } from 'lucide-svelte'
  import { convStore, type Conversation } from '../../stores/conversations.svelte'

  let editingId = $state<string | null>(null)
  let editTitle = $state('')
  let longPressTimer: ReturnType<typeof setTimeout> | null = null

  function handleTabClick(conv: Conversation) {
    if (editingId) return
    convStore.switchTo(conv.id)
  }

  function startRename(conv: Conversation) {
    editingId = conv.id
    editTitle = conv.title
  }

  function commitRename() {
    if (editingId) {
      const title = editTitle.trim()
      if (title) {
        convStore.rename(editingId, title)
      }
      editingId = null
    }
  }

  function cancelRename() {
    editingId = null
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); commitRename() }
    if (e.key === 'Escape') cancelRename()
  }

  function handleArchive(e: MouseEvent, id: string) {
    e.stopPropagation()
    convStore.archive(id)
  }

  function handleMouseDown(e: MouseEvent, id: string) {
    if (e.button === 1) { // middle click
      e.preventDefault()
      convStore.archive(id)
    }
  }

  function handleTouchStart(conv: Conversation) {
    longPressTimer = setTimeout(() => startRename(conv), 500)
  }

  function handleTouchEnd() {
    if (longPressTimer) { clearTimeout(longPressTimer); longPressTimer = null }
  }
</script>

<div class="conv-tabs">
  <div class="conv-tabs-scroll">
    {#each convStore.conversations as conv (conv.id)}
      <button
        class="conv-tab"
        class:active={conv.id === convStore.activeConvId}
        onclick={() => handleTabClick(conv)}
        ondblclick={() => startRename(conv)}
        onauxclick={(e) => handleMouseDown(e, conv.id)}
        ontouchstart={() => handleTouchStart(conv)}
        ontouchend={handleTouchEnd}
        ontouchcancel={handleTouchEnd}
      >
        {#if editingId === conv.id}
          <!-- svelte-ignore a11y_autofocus -->
          <input
            class="conv-tab-edit"
            bind:value={editTitle}
            onblur={commitRename}
            onkeydown={handleKeydown}
            autofocus
          />
        {:else}
          <span class="conv-tab-title">{conv.title || 'Chat'}</span>
          {#if convStore.unreadCounts[conv.id]}
            <span class="conv-tab-badge"></span>
          {/if}
          <span class="conv-tab-spacer"></span>
          {#if convStore.conversations.length > 1}
            <button class="conv-tab-close btn-icon" onclick={(e) => handleArchive(e, conv.id)} title="Archive">
              <X size={12} />
            </button>
          {/if}
        {/if}
      </button>
    {/each}
  </div>
</div>

<style>
  .conv-tabs {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-base);
    min-height: 36px;
  }

  .conv-tabs-scroll {
    display: flex;
    gap: 2px;
    overflow-x: auto;
    flex: 1;
    min-width: 0;
    scrollbar-width: none;
  }
  .conv-tabs-scroll::-webkit-scrollbar { display: none; }

  .conv-tab {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 12px;
    border: none;
    background: transparent;
    color: var(--text-dim);
    font-size: var(--font-xs);
    font-family: inherit;
    cursor: pointer;
    border-radius: 4px 4px 0 0;
    white-space: nowrap;
    min-width: 60px;
    max-width: 180px;
    flex: 1 1 120px;
    position: relative;
    border-bottom: 2px solid transparent;
    transition: background 0.15s, color 0.15s;
  }

  .conv-tab:hover {
    background: var(--bg-input);
    color: var(--text);
  }

  .conv-tab.active {
    color: var(--accent);
    border-bottom-color: var(--accent);
    background: var(--bg-input);
  }

  .conv-tab-title {
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 120px;
  }

  .conv-tab-edit {
    background: var(--bg-input);
    border: 1px solid var(--accent);
    color: var(--text);
    font-size: var(--font-xs);
    font-family: inherit;
    padding: 1px 4px;
    border-radius: 2px;
    width: 100px;
    outline: none;
  }

  .conv-tab-spacer {
    flex: 1;
  }

  .conv-tab-badge {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
    flex-shrink: 0;
  }

  .conv-tab-close {
    visibility: hidden;
    padding: 2px;
    border: none;
    background: transparent;
    color: var(--text-dim);
    cursor: pointer;
    border-radius: 2px;
    line-height: 0;
    flex-shrink: 0;
  }
  .conv-tab-close:hover {
    background: var(--bg-hover);
    color: var(--danger);
  }
  .conv-tab:hover .conv-tab-close,
  .conv-tab.active .conv-tab-close {
    visibility: visible;
  }</style>
