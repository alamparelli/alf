// Reactive avatar URL that refreshes when the avatar SSE event fires.
import { events } from './events.svelte'

class AvatarUrl {
  current = $state('/api/settings/avatar')

  constructor() {
    events.subscribe('avatar', () => {
      // Cache-bust by appending timestamp.
      this.current = `/api/settings/avatar?t=${Date.now()}`
    })
  }
}

export const avatarUrl = new AvatarUrl()
