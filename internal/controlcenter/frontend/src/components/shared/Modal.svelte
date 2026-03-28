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
  let sheetEl: HTMLDivElement
  let dragStartY = 0
  let dragCurrentY = 0
  let dragging = $state(false)

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

  // Sheet drag-to-dismiss
  function onTouchStart(e: TouchEvent) {
    const target = e.target as HTMLElement
    if (target.closest('.modal-sheet-content')?.scrollTop !== 0) return
    dragStartY = e.touches[0].clientY
    dragging = true
  }

  function onTouchMove(e: TouchEvent) {
    if (!dragging) return
    dragCurrentY = e.touches[0].clientY - dragStartY
    if (dragCurrentY < 0) dragCurrentY = 0
    if (sheetEl) sheetEl.style.transform = `translateY(${dragCurrentY}px)`
  }

  function onTouchEnd() {
    if (!dragging) return
    dragging = false
    if (dragCurrentY > 120 && onclose) {
      onclose()
    }
    if (sheetEl) sheetEl.style.transform = ''
    dragCurrentY = 0
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="modal-backdrop" role="presentation" onclick={onclose}>
    <!-- Desktop: centered modal -->
    <div class="modal desktop-modal" class:wide onclick={(e: MouseEvent) => e.stopPropagation()} role="dialog" bind:this={modalEl}>
      {#if onclose}
        <button class="modal-close" onclick={onclose} aria-label="Close">&times;</button>
      {/if}
      {@render children()}
    </div>

    <!-- Mobile: bottom sheet -->
    <div
      class="modal-sheet"
      onclick={(e: MouseEvent) => e.stopPropagation()}
      role="dialog"
      bind:this={sheetEl}
      ontouchstart={onTouchStart}
      ontouchmove={onTouchMove}
      ontouchend={onTouchEnd}
    >
      <div class="sheet-handle"></div>
      <div class="modal-sheet-content">
        {@render children()}
      </div>
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

  /* Desktop modal */
  .desktop-modal {
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

  .desktop-modal.wide {
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

  /* Mobile sheet - hidden on desktop */
  .modal-sheet {
    display: none;
  }

  @media (max-width: 768px) {
    .desktop-modal {
      display: none;
    }

    .modal-backdrop {
      align-items: flex-end;
    }

    .modal-sheet {
      display: flex;
      flex-direction: column;
      width: 100%;
      max-height: 90vh;
      background: var(--bg-card);
      border-radius: 16px 16px 0 0;
      padding: 0 1rem calc(1rem + env(safe-area-inset-bottom, 0px));
      box-shadow: 0 -4px 32px rgba(0, 0, 0, 0.2);
      animation: sheetUp 0.25s ease;
      touch-action: none;
    }

    .sheet-handle {
      width: 36px;
      height: 5px;
      background: var(--text-dim);
      opacity: 0.3;
      border-radius: 3px;
      margin: 10px auto 12px;
      flex-shrink: 0;
    }

    .modal-sheet-content {
      overflow-y: auto;
      flex: 1;
      min-height: 0;
    }

    @keyframes sheetUp {
      from { transform: translateY(100%); }
      to { transform: translateY(0); }
    }
  }
</style>
