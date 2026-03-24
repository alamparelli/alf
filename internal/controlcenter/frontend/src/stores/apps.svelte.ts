import { api } from '../lib/api'

export interface AppInfo {
  name: string
  slug: string
  icon?: string
  description?: string
  has_ui?: boolean
}

class AppsStore {
  items = $state<AppInfo[]>([])
  loaded = $state(false)

  async load() {
    try {
      const data = await api<AppInfo[]>('/api/apps/')
      const list = Array.isArray(data) ? data : (data as any)?.items || []
      this.items = list
      this.loaded = true
    } catch {
      // silent
    }
  }
}

export const apps = new AppsStore()
