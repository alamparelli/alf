<script lang="ts">
  import { onMount } from 'svelte'
  import Sidebar from './components/Sidebar.svelte'
  import BottomNav from './components/BottomNav.svelte'
  import Toast from './components/Toast.svelte'
  import AppFrame from './components/AppFrame.svelte'
  import SettingsView from './views/SettingsView.svelte'
  import HomeView from './views/HomeView.svelte'
  import TasksView from './views/TasksView.svelte'
  import VaultView from './views/VaultView.svelte'
  import LogsView from './views/LogsView.svelte'
  import DocsView from './views/DocsView.svelte'
  import TeamsView from './views/TeamsView.svelte'
  import FirewallView from './views/FirewallView.svelte'
  import MarketplaceView from './views/MarketplaceView.svelte'
  import TiersView from './views/TiersView.svelte'
  import SchedulesView from './views/SchedulesView.svelte'
  import TerminalView from './views/TerminalView.svelte'
  import ChatView from './views/ChatView.svelte'
  import SpotlightSearch from './components/SpotlightSearch.svelte'
  import { nav } from './stores/nav.svelte'
  import { apps } from './stores/apps.svelte'
  import { theme } from './stores/theme.svelte'
  import { toasts } from './stores/toast.svelte'

  onMount(() => {
    apps.load()

    // Listen for SDK messages from iframe apps
    window.addEventListener('message', (e: MessageEvent) => {
      if (e.data?.type !== 'alf-app') return
      if (e.data.action === 'navigate') nav.navigateTo(e.data.view)
      if (e.data.action === 'toast') {
        toasts.show(e.data.msg, e.data.type)
      }
      if (e.data.action === 'ready' && e.source) {
        (e.source as Window).postMessage(
          { type: 'alf', action: 'theme', palette: theme.palette, dark: theme.isDark },
          '*'
        )
      }
    })
  })
</script>

<div class="app-layout">
  <Sidebar />

  {#if nav.sidebarOpen}
    <div class="sidebar-overlay" onclick={() => nav.sidebarOpen = false} role="presentation"></div>
  {/if}

  <div class="main-content">
    <div class="main-header">
      <button class="hamburger-btn" onclick={() => nav.toggleSidebar()} aria-label="Toggle menu">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <line x1="3" y1="6" x2="21" y2="6"></line>
          <line x1="3" y1="12" x2="21" y2="12"></line>
          <line x1="3" y1="18" x2="21" y2="18"></line>
        </svg>
      </button>
    </div>

    <!-- Chat + Terminal always mounted (preserve state) -->
    <div style:display={nav.currentView === 'chat' ? '' : 'none'}>
      <ChatView />
    </div>
    <div style:display={nav.currentView === 'terminal' ? '' : 'none'}>
      <TerminalView />
    </div>

    {#if nav.currentView === 'chat' || nav.currentView === 'terminal'}
      <!-- handled above as always-mounted -->
    {:else if nav.currentView === 'home'}
      <HomeView />
    {:else if nav.currentView === 'tasks'}
      <TasksView />
    {:else if nav.currentView === 'schedules'}
      <SchedulesView />
    {:else if nav.currentView === 'teams'}
      <TeamsView />
    {:else if nav.currentView === 'tiers'}
      <TiersView />
    {:else if nav.currentView === 'firewall'}
      <FirewallView />
    {:else if nav.currentView === 'vault'}
      <VaultView />
    {:else if nav.currentView === 'marketplace'}
      <MarketplaceView />
    {:else if nav.currentView === 'settings'}
      <SettingsView />
    {:else if nav.currentView === 'logs'}
      <LogsView />
    {:else if nav.currentView === 'docs'}
      <DocsView />
    {:else if nav.currentView.startsWith('docs:')}
      <DocsView articleId={nav.currentView.slice(5)} />
    {:else if nav.currentView.startsWith('page:')}
      <AppFrame slug={nav.currentView.slice(5)} />
    {:else}
      <div class="placeholder-view"><h2>Unknown view</h2></div>
    {/if}
  </div>

  <BottomNav />
</div>

<Toast />
<SpotlightSearch />

<style>
  :global(:root) {
    --sidebar-width: 220px;
  }

  :global(*) {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
  }

  :global(body) {
    font-family: 'Work Sans', -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
    background: var(--bg);
    color: var(--text);
    line-height: 1.6;
    min-height: 100vh;
  }

  :global(h1, h2, h3, h4) {
    font-family: 'Sora', sans-serif;
  }

  :global(::-webkit-scrollbar) { width: 6px; height: 6px; }
  :global(::-webkit-scrollbar-track) { background: transparent; }
  :global(::-webkit-scrollbar-thumb) { background: var(--border); border-radius: 3px; }
  :global(::-webkit-scrollbar-thumb:hover) { background: var(--text-dim); }

  .app-layout {
    display: flex;
    min-height: 100vh;
  }

  .main-content {
    flex: 1;
    margin-left: var(--sidebar-width);
    padding: 0 24px 24px;
    overflow-y: auto;
    min-height: 100vh;
  }

  .main-header {
    padding: 12px 0;
    display: flex;
    align-items: center;
  }

  .hamburger-btn {
    display: none;
    background: none;
    border: none;
    color: var(--text);
    cursor: pointer;
    padding: 4px;
  }

  .sidebar-overlay {
    display: none;
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.4);
    z-index: 19;
  }

  /* Shared view layout — aligned with Marketplace style */
  :global(.view-layout) {
    width: 100%;
    padding: 8px 0;
  }

  :global(.view-layout h2) {
    margin-bottom: 16px;
  }

  :global(.view-header) {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1rem;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  :global(.view-header h2) {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin: 0;
    font-size: 1.25rem;
  }

  /* Common button styles */
  :global(.btn) {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    text-decoration: none;
    transition: background 0.15s;
  }

  :global(.btn:hover) {
    background: var(--border);
  }

  :global(.btn-primary) {
    background: var(--accent);
    color: var(--on-accent);
    border-color: var(--accent);
  }

  :global(.btn-primary:hover) {
    opacity: 0.9;
  }

  :global(.btn-ghost) {
    background: transparent;
    border-color: transparent;
  }

  :global(.btn-ghost:hover) {
    background: var(--bg-input);
  }

  :global(.btn-sm) {
    padding: 4px 10px;
    font-size: 0.75rem;
  }

  :global(.btn:disabled) {
    opacity: 0.5;
    cursor: not-allowed;
  }

  :global(.btn-icon) {
    padding: 6px;
    border: none;
    background: none;
    color: var(--text-dim);
    cursor: pointer;
  }

  :global(.btn-icon:hover) {
    color: var(--text);
  }

  /* Common input styles */
  :global(.input) {
    width: 100%;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
  }

  /* Additional button variants */
  :global(.btn-success) {
    background: var(--green);
    color: #fff;
    border-color: var(--green);
  }

  :global(.btn-warning) {
    background: var(--yellow);
    color: #fff;
    border-color: var(--yellow);
  }

  :global(.btn-info) {
    background: var(--sapphire);
    color: #fff;
    border-color: var(--sapphire);
  }

  :global(.btn-danger) {
    background: var(--red);
    color: #fff;
    border-color: var(--red);
  }

  :global(.btn-secondary) {
    background: var(--bg-input);
    color: var(--text);
    border-color: var(--border);
  }

  :global(.btn-secondary-sm) {
    background: var(--bg-input);
    color: var(--text);
    border-color: var(--border);
    padding: 4px 10px;
    font-size: 0.75rem;
  }

  /* Common select styles */
  :global(select) {
    padding: 6px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
  }

  /* Common textarea styles */
  :global(textarea) {
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
    resize: vertical;
  }

  /* Common form patterns */
  :global(.form-group) {
    margin-bottom: 12px;
  }

  :global(.form-group label) {
    display: block;
    font-size: 0.8rem;
    font-weight: 500;
    margin-bottom: 4px;
  }

  .placeholder-view {
    padding: 40px 0;
    text-align: center;
    color: var(--text-dim);
  }

  .placeholder-view h2 {
    margin-bottom: 8px;
    color: var(--text);
  }

  @media (max-width: 768px) {
    .main-content {
      margin-left: 0;
    }
    .hamburger-btn {
      display: block;
    }
    .sidebar-overlay {
      display: block;
    }
  }
</style>
