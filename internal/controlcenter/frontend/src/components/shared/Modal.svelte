<script lang="ts">
  import type { Snippet } from 'svelte'

  let { open = false, onclose, children }: {
    open?: boolean
    onclose?: () => void
    children: Snippet
  } = $props()
</script>

{#if open}
  <div class="modal-backdrop" onclick={onclose} role="presentation">
    <div class="modal" onclick={(e: MouseEvent) => e.stopPropagation()} role="dialog">
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
    max-height: 80vh;
    overflow-y: auto;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
  }
</style>
