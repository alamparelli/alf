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
  import DeveloperView from './views/DeveloperView.svelte'
  import SkillsView from './views/SkillsView.svelte'
  import TiersView from './views/TiersView.svelte'
  import SchedulesView from './views/SchedulesView.svelte'
  import TerminalView from './views/TerminalView.svelte'
  import ChatView from './views/ChatView.svelte'
  import SpotlightSearch from './components/SpotlightSearch.svelte'
  import SetupWizard from './components/SetupWizard.svelte'
  import { X, Search } from 'lucide-svelte'
  import { nav, SYSTEM_TABS } from './stores/nav.svelte'
  import { apps } from './stores/apps.svelte'
  import { events } from './stores/events.svelte'
  import { theme } from './stores/theme.svelte'
  import { toasts } from './stores/toast.svelte'
  import { sound } from './stores/sound.svelte'
  import { spotlightSettings } from './stores/spotlight.svelte'
  import { api } from './lib/api'

  let showWizard = $state(false)
  let wizardRef: any = $state(null)

  onMount(() => {
    apps.load()
    events.connect()
    events.subscribe('apps', () => apps.load())
    events.subscribe('marketplace', () => apps.load())
    events.subscribe('new_message', () => {
      sound.play()
    })

    // Auto-show setup wizard on first visit if setup is incomplete
    if (!localStorage.getItem('alf-welcomed')) {
      api<any>('/api/setup/status').then(status => {
        if (!status.completed) {
          wizardRef?.setMode('wizard')
          showWizard = true
        } else {
          wizardRef?.setMode('welcome')
          showWizard = true
        }
      }).catch(() => {
        wizardRef?.setMode('welcome')
        showWizard = true
      })
    }

    // Listen for setup wizard re-run from Settings
    window.addEventListener('alf:open-wizard', () => {
      wizardRef?.setMode('wizard')
      showWizard = true
    })

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

  <div class="main-content" class:main-content--app={nav.currentView.startsWith('page:')}>
    <div class="main-header">
      <button class="hamburger-btn" onclick={() => nav.toggleSidebar()} aria-label="Toggle menu">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <line x1="3" y1="6" x2="21" y2="6"></line>
          <line x1="3" y1="12" x2="21" y2="12"></line>
          <line x1="3" y1="18" x2="21" y2="18"></line>
        </svg>
      </button>
    </div>

    <!-- Open tabs bar -->
    {#if nav.openTabs.length > 0}
      <div class="open-tabs-bar">
        {#each nav.openTabs as tab (tab.id)}
          <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
          <div
            class="open-tab"
            class:active={nav.currentView === tab.view}
            onclick={() => nav.navigateTo(tab.view)}
          >
            <span class="open-tab-label">{tab.label}</span>
            <button class="open-tab-close" onclick={(e) => { e.stopPropagation(); nav.closeTab(tab.id) }}>
              <X size={11} />
            </button>
          </div>
        {/each}
      </div>
    {/if}

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
    {:else if nav.currentView === 'skills'}
      <SkillsView />
    {:else if nav.currentView === 'tiers'}
      <TiersView />
    {:else if nav.currentView === 'firewall'}
      <FirewallView />
    {:else if nav.currentView === 'vault'}
      <VaultView />
    {:else if nav.currentView === 'marketplace'}
      <MarketplaceView />
    {:else if nav.currentView === 'developer'}
      <DeveloperView />
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

{#if nav.currentView !== 'chat'}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <button class="spotlight-fab" onclick={() => window.dispatchEvent(new CustomEvent('alf:open-spotlight'))}>
    <Search size={18} />
    <span class="fab-tooltip">Search <kbd>{navigator?.platform?.includes('Mac') ? '⌘' : 'Ctrl'}+{spotlightSettings.shortcutKey.toUpperCase()}</kbd></span>
  </button>
{/if}

<Toast />
<SpotlightSearch />
<SetupWizard bind:open={showWizard} bind:this={wizardRef} />

<style>
  :global(:root) {
    --sidebar-width: 220px;

    /* Spacing tokens */
    --space-xs: 4px;
    --space-sm: 8px;
    --space-md: 16px;
    --space-lg: 24px;
    --space-xl: 32px;

    /* Typography tokens */
    --font-xs: 11px;
    --font-sm: 13px;
    --font-md: 15px;
    --font-lg: 18px;
    --font-xl: 24px;

    /* Shadow tokens */
    --shadow-sm: 0 1px 2px rgba(0,0,0,0.08);
    --shadow-md: 0 4px 12px rgba(0,0,0,0.12);
    --shadow-lg: 0 8px 24px rgba(0,0,0,0.16);
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

  .main-content--app {
    padding: 0;
    position: relative;
    overflow: hidden;
  }

  .main-content--app .main-header {
    display: none;
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

  :global(.btn-lg) {
    padding: 12px 24px;
    font-size: 0.9rem;
    min-height: 44px;
  }

  :global(.btn-block) {
    width: 100%;
  }

  :global(.btn-group) {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
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

  /* ── AIG shared components ──*/

  :global(.toolbar) {
    display: flex;
    align-items: center;
    gap: var(--space-sm, 8px);
    margin-bottom: var(--space-md, 16px);
    flex-wrap: wrap;
  }

  :global(.search-box) {
    display: flex;
    align-items: center;
    gap: 6px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    padding: 4px 10px;
    background: var(--bg-card);
    transition: border-color 0.15s;
  }

  :global(.search-box:focus-within) {
    border-color: var(--accent);
  }

  :global(.search-box input) {
    border: none;
    background: none;
    color: var(--text);
    font-size: 0.85rem;
    font-family: inherit;
    outline: none;
    min-width: 120px;
  }

  :global(.search-box svg) {
    color: var(--text-dim);
    flex-shrink: 0;
  }

  :global(.filter-tabs) {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-bottom: 16px;
  }

  :global(.tab) {
    padding: 4px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: none;
    color: var(--text-dim);
    font-family: inherit;
    font-size: 0.75rem;
    cursor: pointer;
    transition: all 0.15s;
  }

  :global(.tab:hover) {
    background: var(--bg-input);
  }

  :global(.tab.active) {
    background: var(--accent);
    color: var(--on-accent);
    border-color: var(--accent);
  }

  :global(.badge) {
    display: inline-flex;
    align-items: center;
    padding: 2px 8px;
    font-size: 0.7rem;
    font-weight: 600;
    border-radius: 4px;
    text-transform: uppercase;
    letter-spacing: 0.03em;
  }

  :global(.badge-success) {
    background: color-mix(in srgb, var(--green) 15%, transparent);
    color: var(--green);
  }

  :global(.badge-danger) {
    background: color-mix(in srgb, var(--red) 15%, transparent);
    color: var(--red);
  }

  :global(.badge-warning) {
    background: color-mix(in srgb, var(--yellow) 15%, transparent);
    color: var(--yellow);
  }

  :global(.badge-info) {
    background: color-mix(in srgb, var(--sapphire) 15%, transparent);
    color: var(--sapphire);
  }

  :global(.badge-accent) {
    background: color-mix(in srgb, var(--accent) 15%, transparent);
    color: var(--accent);
  }

  :global(.alert) {
    padding: 8px 16px;
    border-radius: var(--radius, 8px);
    font-size: 0.85rem;
    border: 1px solid var(--border);
    background: var(--bg-card);
  }

  :global(.alert-success) {
    background: color-mix(in srgb, var(--green) 12%, var(--bg));
    color: var(--green);
    border-color: color-mix(in srgb, var(--green) 25%, transparent);
  }

  :global(.alert-danger) {
    background: color-mix(in srgb, var(--red) 12%, var(--bg));
    color: var(--red);
    border-color: color-mix(in srgb, var(--red) 25%, transparent);
  }

  :global(.alert-warning) {
    background: color-mix(in srgb, var(--yellow) 12%, var(--bg));
    color: var(--yellow);
    border-color: color-mix(in srgb, var(--yellow) 25%, transparent);
  }

  :global(.alert-info) {
    background: color-mix(in srgb, var(--accent) 10%, var(--bg));
    color: var(--text-dim);
    border-color: var(--border);
  }

  :global(.meta) {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 0.75rem;
    color: var(--text-dim);
  }

  @keyframes -global-spin {
    to { transform: rotate(360deg); }
  }

  :global(.spin) {
    animation: spin 1s linear infinite;
  }

  :global(.empty-state) {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 32px 16px;
    color: var(--text-dim);
    text-align: center;
    gap: 8px;
  }

  :global(.loading-state) {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    gap: 8px;
    color: var(--text-dim);
    font-size: 0.85rem;
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

  /* Open tabs bar */
  .open-tabs-bar {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 0 8px;
    overflow-x: auto;
    flex-shrink: 0;
  }
  .open-tabs-bar::-webkit-scrollbar { height: 0; }

  .open-tab {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    background: var(--bg-card);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text-dim);
    font-family: inherit;
    font-size: 0.75rem;
    cursor: pointer;
    white-space: nowrap;
    transition: background 0.15s, color 0.15s;
  }

  .open-tab:hover {
    background: var(--bg-input);
    color: var(--text);
  }

  .open-tab.active {
    color: var(--text);
    border-color: var(--accent);
    font-weight: 500;
  }

  .open-tab-label {
    pointer-events: none;
  }

  .open-tab-close {
    display: flex;
    align-items: center;
    padding: 1px;
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    border-radius: 3px;
    opacity: 0;
    transition: opacity 0.15s;
  }

  .open-tab:hover .open-tab-close {
    opacity: 0.7;
  }

  .open-tab-close:hover {
    opacity: 1 !important;
    background: var(--border);
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

  :global(.spotlight-fab) {
    position: fixed;
    bottom: 24px;
    right: 24px;
    z-index: 900;
    width: 44px;
    height: 44px;
    border-radius: 50%;
    background: var(--accent);
    color: var(--bg);
    border: none;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25);
    transition: transform 0.15s, box-shadow 0.15s;
  }

  :global(.spotlight-fab:hover) {
    transform: scale(1.08);
    box-shadow: 0 6px 16px rgba(0, 0, 0, 0.3);
  }

  :global(.fab-tooltip) {
    position: absolute;
    right: 52px;
    white-space: nowrap;
    background: var(--bg-card, #2a2a2a);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 4px 10px;
    font-size: 0.75rem;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.15s;
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
  }

  :global(.fab-tooltip kbd) {
    background: var(--bg-input);
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 0 4px;
    font-family: 'JetBrains Mono', monospace;
    font-size: 0.7rem;
    margin-left: 4px;
  }

  :global(.spotlight-fab:hover .fab-tooltip) {
    opacity: 1;
  }

  @media (max-width: 768px) {
    :global(.spotlight-fab) {
      bottom: 72px;
      right: 16px;
    }
  }
</style>
