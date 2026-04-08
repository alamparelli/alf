import { api } from '../lib/api'

export interface Conversation {
  id: string
  title: string
  msg_count: number
  created_at: string
  updated_at: string
}

const LS_CONVID = 'alf-chat-convid'
const LS_UNREAD = 'alf-chat-unread'

class ConversationStore {
  conversations = $state<Conversation[]>([])
  activeConvId = $state(localStorage.getItem(LS_CONVID) || '')
  unreadCounts = $state<Record<string, number>>(
    JSON.parse(localStorage.getItem(LS_UNREAD) || '{}')
  )
  loaded = $state(false)
  clientId = Math.random().toString(36).slice(2, 10)

  /** Load conversations from server. Sets activeConvId from server truth. */
  async load() {
    try {
      const data = await api<any>('/api/chat/conversations')
      const all: Conversation[] = data.conversations || []
      this.conversations = all

      const serverActive = data.active_conv_id
      if (serverActive && all.some(c => c.id === serverActive)) {
        this.activeConvId = serverActive
      } else if (all.length > 0) {
        // Most recent (server returns ASC)
        this.activeConvId = all[all.length - 1].id
      }
      this.persistConvId()
      this.loaded = true
    } catch { /* server not ready */ }
  }

  /** Switch to a conversation. */
  async switchTo(id: string) {
    if (id === this.activeConvId) return
    this.activeConvId = id
    this.clearUnread(id)
    this.persistConvId()
  }

  /** Create a new conversation and switch to it. */
  async create() {
    const id = Math.random().toString(36).slice(2, 10)
    try {
      await api('/api/chat/conversations', {
        method: 'POST',
        body: JSON.stringify({ id, title: 'Chat' }),
      })
      this.conversations = [...this.conversations, {
        id, title: 'Chat', msg_count: 0,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      }]
      this.activeConvId = id
      this.persistConvId()
      return id
    } catch {
      return null
    }
  }

  /** Rename a conversation. */
  async rename(id: string, title: string) {
    if (!title.trim()) return false
    try {
      await api(`/api/chat/conversations/${id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title }),
      })
      this.conversations = this.conversations.map(c =>
        c.id === id ? { ...c, title } : c
      )
      return true
    } catch {
      return false
    }
  }

  /** Archive a conversation. Switches to adjacent tab. */
  async archive(id: string) {
    try {
      await api(`/api/chat/conversations/${id}`, { method: 'DELETE' })
      const idx = this.conversations.findIndex(c => c.id === id)
      this.conversations = this.conversations.filter(c => c.id !== id)
      delete this.unreadCounts[id]
      this.persistUnread()

      if (id === this.activeConvId) {
        if (this.conversations.length > 0) {
          // Switch to adjacent: prefer next, then previous
          const newIdx = Math.min(idx, this.conversations.length - 1)
          this.activeConvId = this.conversations[newIdx].id
          this.persistConvId()
        } else {
          // Last conversation — create new
          await this.create()
        }
      }
      return true
    } catch {
      return false
    }
  }

  markUnread(id: string) {
    this.unreadCounts = { ...this.unreadCounts, [id]: (this.unreadCounts[id] || 0) + 1 }
    this.persistUnread()
  }

  clearUnread(id: string) {
    if (this.unreadCounts[id]) {
      const { [id]: _, ...rest } = this.unreadCounts
      this.unreadCounts = rest
      this.persistUnread()
    }
  }

  /** Timestamp of last local conv switch — poll sync ignores server for a grace period after this. */
  lastLocalSwitch = 0

  private persistConvId() {
    localStorage.setItem(LS_CONVID, this.activeConvId)
    this.lastLocalSwitch = Date.now()
    api('/api/chat/active', {
      method: 'PUT',
      body: JSON.stringify({ conv_id: this.activeConvId, client_id: this.clientId }),
    }).catch(() => {})
  }

  private persistUnread() {
    localStorage.setItem(LS_UNREAD, JSON.stringify(this.unreadCounts))
  }
}

export const convStore = new ConversationStore()
