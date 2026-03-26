export interface NavItem {
  view: string
  label: string
  icon: string
  comingSoon?: boolean
}

export interface OpenTab {
  id: string
  view: string
  label: string
  icon: string
}

export const SYSTEM_TABS: NavItem[] = [
  { view: 'chat', label: 'Chat', icon: 'message-circle' },
  { view: 'home', label: 'Workspace', icon: 'folder-open' },
  { view: 'terminal', label: 'Terminal', icon: 'terminal' },
  { view: 'tasks', label: 'Tasks', icon: 'layers' },
  { view: 'schedules', label: 'Schedules', icon: 'calendar-clock' },
  { view: 'teams', label: 'Teams', icon: 'users' },
  { view: 'skills', label: 'Skills', icon: 'zap' },
  { view: 'tiers', label: 'Tiers', icon: 'sliders-horizontal' },
  { view: 'firewall', label: 'Firewall', icon: 'shield' },
  { view: 'vault', label: 'Vault', icon: 'lock' },
  { view: 'marketplace', label: 'Marketplace', icon: 'store' },
  { view: 'settings', label: 'Settings', icon: 'settings' },
  { view: 'logs', label: 'Logs', icon: 'scroll-text' },
  { view: 'docs', label: 'Docs', icon: 'book-open' },
]

class NavStore {
  currentView = $state(localStorage.getItem('alf-view') || 'chat')
  sidebarOpen = $state(false)

  // Badges: view -> count (0 = hidden, >0 = shown)
  badges = $state<Record<string, number>>({})

  // Open tabs (bookmark-style)
  openTabs = $state<OpenTab[]>(
    JSON.parse(localStorage.getItem('alf-open-tabs') || '[]')
  )

  favorites = $state<string[]>(
    JSON.parse(localStorage.getItem('alf-nav-favs') || '[]')
  )

  collapsed = $state<Record<string, boolean>>(
    JSON.parse(localStorage.getItem('alf-nav-collapsed') || '{}')
  )

  navigateTo(view: string) {
    this.currentView = view
    localStorage.setItem('alf-view', view)
    this.sidebarOpen = false
    // Clear badge when navigating to the view
    if (this.badges[view]) {
      this.badges[view] = 0
    }
  }

  openTab(view: string, label: string, icon: string) {
    // Don't duplicate
    if (this.openTabs.some(t => t.view === view)) {
      this.navigateTo(view)
      return
    }
    const id = view + '-' + Date.now()
    this.openTabs = [...this.openTabs, { id, view, label, icon }]
    localStorage.setItem('alf-open-tabs', JSON.stringify(this.openTabs))
    this.navigateTo(view)
  }

  closeTab(id: string) {
    const tab = this.openTabs.find(t => t.id === id)
    this.openTabs = this.openTabs.filter(t => t.id !== id)
    localStorage.setItem('alf-open-tabs', JSON.stringify(this.openTabs))
    // If closing the active tab, navigate to first remaining tab or chat
    if (tab && tab.view === this.currentView) {
      const next = this.openTabs[0]
      this.navigateTo(next ? next.view : 'chat')
    }
  }

  closeAllTabs() {
    this.openTabs = []
    localStorage.setItem('alf-open-tabs', '[]')
  }

  setBadge(view: string, count: number) {
    this.badges[view] = count
  }

  incrementBadge(view: string) {
    this.badges[view] = (this.badges[view] || 0) + 1
  }

  toggleSection(section: string) {
    this.collapsed[section] = !this.collapsed[section]
    localStorage.setItem('alf-nav-collapsed', JSON.stringify(this.collapsed))
  }

  toggleFavorite(view: string) {
    const idx = this.favorites.indexOf(view)
    if (idx >= 0) {
      this.favorites.splice(idx, 1)
    } else {
      this.favorites.push(view)
    }
    localStorage.setItem('alf-nav-favs', JSON.stringify(this.favorites))
  }

  toggleSidebar() {
    this.sidebarOpen = !this.sidebarOpen
  }
}

export const nav = new NavStore()
