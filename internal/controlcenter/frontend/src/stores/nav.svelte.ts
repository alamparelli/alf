export interface NavItem {
  view: string
  label: string
  icon: string
}

export const SYSTEM_TABS: NavItem[] = [
  { view: 'chat', label: 'Chat', icon: 'message-circle' },
  { view: 'home', label: 'Home', icon: 'home' },
  { view: 'terminal', label: 'Terminal', icon: 'terminal' },
  { view: 'tasks', label: 'Tasks', icon: 'layers' },
  { view: 'schedules', label: 'Schedules', icon: 'calendar-clock' },
  { view: 'teams', label: 'Teams', icon: 'users' },
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
