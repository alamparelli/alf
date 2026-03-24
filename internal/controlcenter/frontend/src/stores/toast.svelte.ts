export interface ToastItem {
  id: number
  msg: string
  type: 'success' | 'error' | 'info'
}

let nextId = 0

class ToastStore {
  items = $state<ToastItem[]>([])

  show(msg: string, type: 'success' | 'error' | 'info' = 'success') {
    const id = nextId++
    this.items.push({ id, msg, type })
    if (type === 'error') console.error('[toast]', msg)
    setTimeout(() => {
      this.items = this.items.filter(t => t.id !== id)
    }, 3000)
  }
}

export const toasts = new ToastStore()
