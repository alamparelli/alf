<script lang="ts">
  import { nav, SYSTEM_TABS } from '../stores/nav.svelte'
  import { apps } from '../stores/apps.svelte'
  import {
    MessageCircle, Home, Terminal, Layers, CalendarClock,
    Users, SlidersHorizontal, Shield, Lock, Store,
    Settings, ScrollText, BookOpen, ChevronDown, Pin, Package
  } from 'lucide-svelte'
  import { getIcon } from '../lib/icons'

  const ICON_MAP: Record<string, any> = {
    'message-circle': MessageCircle,
    'home': Home,
    'terminal': Terminal,
    'layers': Layers,
    'calendar-clock': CalendarClock,
    'users': Users,
    'sliders-horizontal': SlidersHorizontal,
    'shield': Shield,
    'lock': Lock,
    'store': Store,
    'settings': Settings,
    'scroll-text': ScrollText,
    'book-open': BookOpen,
  }
</script>

<aside class="sidebar" class:open={nav.sidebarOpen}>
  <div class="sidebar-header">
    <h1><span>ALF</span> OS<span class="sidebar-dot"></span></h1>
  </div>

  <nav class="sidebar-nav">
    <!-- System section -->
    <div class="nav-section" class:collapsed={nav.collapsed['system']}>
      <button class="nav-section-toggle" onclick={() => nav.toggleSection('system')}>
        <ChevronDown size={12} class="nav-chevron" />
        <span>System</span>
      </button>
      <div class="nav-section-items">
        {#each SYSTEM_TABS as tab}
          {@const isFav = nav.favorites.includes(tab.view)}
          <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
          <div
            class="nav-item"
            class:active={nav.currentView === tab.view}
            class:nav-fav={isFav}
            onclick={() => nav.navigateTo(tab.view)}
          >
            {#if ICON_MAP[tab.icon]}
              <svelte:component this={ICON_MAP[tab.icon]} size={16} />
            {/if}
            <span>{tab.label}</span>
            {#if nav.badges[tab.view]}
              <span class="nav-badge">{nav.badges[tab.view]}</span>
            {/if}
            <button
              class="nav-fav-btn"
              onclick={(e: MouseEvent) => { e.stopPropagation(); nav.toggleFavorite(tab.view) }}
              title={isFav ? 'Unpin' : 'Pin'}
            >
              <Pin size={12} />
            </button>
          </div>
        {/each}
      </div>
    </div>

    <!-- Apps section -->
    {#if apps.items.length > 0}
      <div class="nav-section" class:collapsed={nav.collapsed['apps']}>
        <button class="nav-section-toggle" onclick={() => nav.toggleSection('apps')}>
          <ChevronDown size={12} class="nav-chevron" />
          <span>Apps</span>
        </button>
        <div class="nav-section-items">
          {#each apps.items as app}
            {@const appView = 'page:' + app.name}
            {@const isFav = nav.favorites.includes(appView)}
            <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
            <div
              class="nav-item"
              class:active={nav.currentView === appView}
              class:nav-fav={isFav}
              onclick={() => nav.navigateTo(appView)}
            >
              {#if getIcon(app.icon)}
                <svelte:component this={getIcon(app.icon)} size={16} />
              {:else}
                <Package size={16} />
              {/if}
              <span>{app.display_name || app.name}</span>
              <button
                class="nav-fav-btn"
                onclick={(e: MouseEvent) => { e.stopPropagation(); nav.toggleFavorite(appView) }}
                title={isFav ? 'Unpin' : 'Pin'}
              >
                <Pin size={12} />
              </button>
            </div>
          {/each}
        </div>
      </div>
    {/if}
  </nav>

  <div class="sidebar-bottom">
    <div class="sidebar-footer">
      <span class="sidebar-footer-text">Made with &hearts;</span>
      <button class="sidebar-more" onclick={() => nav.navigateTo('settings')}>more</button>
    </div>
  </div>
</aside>

<style>
  .sidebar {
    width: var(--sidebar-width);
    min-width: var(--sidebar-width);
    background: var(--bg-card);
    border-right: 1px solid var(--border);
    display: flex;
    flex-direction: column;
    position: fixed;
    top: 0;
    left: 0;
    bottom: 0;
    z-index: 20;
  }

  .sidebar-header {
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
    display: flex;
    align-items: center;
  }

  .sidebar-header h1 {
    font-family: 'Sora', sans-serif;
    font-size: 1.2rem;
    font-weight: 700;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .sidebar-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--accent);
    display: inline-block;
  }

  .sidebar-nav {
    flex: 1;
    overflow-y: auto;
    padding: 8px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-height: 0;
  }

  .nav-section {
    margin-bottom: 4px;
  }

  .nav-section + .nav-section {
    border-top: 1px solid var(--border);
    padding-top: 8px;
    margin-top: 4px;
  }

  .nav-section-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 6px 12px;
    background: none;
    border: none;
    cursor: pointer;
    font-family: 'Sora', 'JetBrains Mono', monospace;
    font-size: 0.7rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--accent);
    transition: color 0.15s;
  }

  .nav-section-toggle:hover {
    color: var(--text);
  }

  :global(.nav-section-toggle .nav-chevron) {
    transition: transform 0.2s;
  }

  .nav-section.collapsed :global(.nav-chevron) {
    transform: rotate(-90deg);
  }

  .nav-section-items {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .nav-section.collapsed .nav-section-items .nav-item:not(.nav-fav) {
    display: none;
  }

  .nav-item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 12px;
    border-left: 3px solid transparent;
    border-radius: 0 6px 6px 0;
    color: var(--text-dim);
    text-decoration: none;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s, color 0.15s, border-color 0.15s;
    position: relative;
  }

  .nav-item:hover {
    background: rgba(255, 255, 255, 0.06);
    color: var(--text);
  }

  .nav-item.active {
    border-left-color: var(--accent);
    color: var(--text);
    background: rgba(255, 255, 255, 0.06);
  }

  .nav-badge {
    background: var(--accent);
    color: var(--on-accent, #fff);
    font-size: 0.6rem;
    font-weight: 700;
    min-width: 16px;
    height: 16px;
    border-radius: 8px;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 0 4px;
    line-height: 1;
  }

  .nav-fav-btn {
    margin-left: auto;
    padding: 0;
    background: none;
    border: none;
    color: var(--text-dim);
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.15s, color 0.15s;
    display: flex;
    align-items: center;
  }

  .nav-item:hover .nav-fav-btn {
    opacity: 0.5;
  }

  .nav-fav-btn:hover {
    opacity: 1 !important;
    color: var(--accent);
  }

  .nav-item.nav-fav .nav-fav-btn {
    opacity: 0.7;
    color: var(--accent);
  }

  .sidebar-bottom {
    flex-shrink: 0;
  }

  .sidebar-footer {
    padding: 12px 20px;
    border-top: 1px solid var(--border);
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 0.7rem;
    color: var(--text-dim);
  }

  .sidebar-more {
    color: var(--accent);
    cursor: pointer;
    text-decoration: none;
    font-weight: 500;
  }

  .sidebar-more:hover {
    text-decoration: underline;
  }

  @media (max-width: 768px) {
    .sidebar {
      transform: translateX(-100%);
      transition: transform 0.2s ease;
    }
    .sidebar.open {
      transform: translateX(0);
    }
  }
</style>
