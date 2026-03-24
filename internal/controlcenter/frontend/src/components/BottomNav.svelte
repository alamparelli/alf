<script lang="ts">
  import { nav } from '../stores/nav.svelte'
  import { MessageCircle, MoreHorizontal } from 'lucide-svelte'
</script>

<nav class="bottom-nav">
  <button
    class="bottom-nav-item"
    class:active={nav.currentView === 'chat'}
    onclick={() => nav.navigateTo('chat')}
  >
    <MessageCircle size={20} />
    <span>Chat</span>
  </button>
  <button
    class="bottom-nav-item"
    onclick={() => nav.toggleSidebar()}
  >
    <MoreHorizontal size={20} />
    <span>More</span>
  </button>
</nav>

<style>
  .bottom-nav {
    display: none;
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    background: var(--bg-card);
    border-top: 1px solid var(--border);
    padding: 6px 0 env(safe-area-inset-bottom, 6px);
    justify-content: space-around;
    z-index: 30;
  }

  .bottom-nav-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    padding: 4px 16px;
    font-size: 0.65rem;
    font-family: inherit;
  }

  .bottom-nav-item.active {
    color: var(--accent);
  }

  @media (max-width: 768px) {
    .bottom-nav {
      display: flex;
    }
  }
</style>
