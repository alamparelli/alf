<script lang="ts">
  import { toasts } from '../stores/toast.svelte'
</script>

{#if toasts.items.length > 0}
  <div class="toast-container">
    {#each toasts.items as item (item.id)}
      <div class="toast {item.type}">{item.msg}</div>
    {/each}
  </div>
{/if}

<style>
  .toast-container {
    position: fixed;
    top: 16px;
    left: 50%;
    transform: translateX(-50%);
    z-index: 9999;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }

  .toast {
    padding: 10px 20px;
    border-radius: var(--radius, 8px);
    font-size: var(--font-sm, 14px);
    font-weight: 500;
    color: var(--on-accent, #fff);
    background: var(--accent);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.25);
    animation: toast-in 0.2s ease;
  }

  .toast.success {
    background: var(--accent);
    color: var(--on-accent, #fff);
  }

  .toast.error {
    background: var(--red, #e53e3e);
    color: #fff;
  }

  .toast.info {
    background: var(--bg-card);
    color: var(--text);
    border: 1px solid var(--border);
  }

  @keyframes toast-in {
    from { opacity: 0; transform: translateY(-8px); }
    to { opacity: 1; transform: translateY(0); }
  }
</style>
