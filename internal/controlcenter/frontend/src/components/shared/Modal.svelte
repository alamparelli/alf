<script lang="ts">
  import { tick } from 'svelte'
  import type { Snippet } from 'svelte'

  let { open = false, wide = false, onclose, children }: {
    open?: boolean
    wide?: boolean
    onclose?: () => void
    children: Snippet
  } = $props()

  let modalEl: HTMLDivElement

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
</style>
