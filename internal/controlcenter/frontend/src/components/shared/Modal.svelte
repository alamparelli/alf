<script lang="ts">
  import { tick, onMount, onDestroy } from 'svelte'
  import type { Snippet } from 'svelte'

  let { open = false, wide = false, onclose, children }: {
    open?: boolean
    wide?: boolean
    onclose?: () => void
    children: Snippet
  } = $props()

  let modalEl: HTMLDivElement

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && open && onclose) {
      e.preventDefault()
      e.stopPropagation()
      onclose()
    }
  }

  onMount(() => window.addEventListener('keydown', handleKeydown))
  onDestroy(() => window.removeEventListener('keydown', handleKeydown))

  // Auto-focus the first input/textarea/select when modal opens.
  $effect(() => {
    if (open && modalEl) {
      tick().then(() => {
        const input = modalEl.querySelector('input, textarea, select') as HTMLElement
        input?.focus()
      })
    }
  })
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="modal-backdrop" role="presentation">
    <div class="modal" class:wide onclick={(e: MouseEvent) => e.stopPropagation()} role="dialog" bind:this={modalEl}>
      {#if onclose}
        <button class="modal-close" onclick={onclose} aria-label="Close">&times;</button>
      {/if}
      {@render children()}
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
  }

  .modal {
    position: relative;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    padding: 1.5rem;
    min-width: 360px;
    max-width: 90vw;
    max-height: 85vh;
    overflow-y: auto;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
  }

  .modal.wide {
    width: 80vw;
    max-width: 1000px;
  }

  .modal-close {
    position: absolute;
    top: 0.75rem;
    right: 0.75rem;
    background: none;
    border: none;
    color: var(--text-dim);
    font-size: 1.4rem;
    line-height: 1;
    cursor: pointer;
    padding: 0.25rem 0.5rem;
    border-radius: var(--radius, 8px);
    transition: color 0.15s, background 0.15s;
  }

  .modal-close:hover {
    color: var(--text);
    background: var(--bg-input);
  }
</style>
