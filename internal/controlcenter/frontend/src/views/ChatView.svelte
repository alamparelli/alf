<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import { X, MessageCircle, RotateCw, Play } from 'lucide-svelte'
  import ChatMessageComponent from '../components/chat/ChatMessage.svelte'
  import ChatInput from '../components/chat/ChatInput.svelte'
  import Modal from '../components/shared/Modal.svelte'
  import Toggle from '../components/shared/Toggle.svelte'
  import { api } from '../lib/api'
  import { toasts } from '../stores/toast.svelte'
  import { nav } from '../stores/nav.svelte'
  import { sound } from '../stores/sound.svelte'
  import { events } from '../stores/events.svelte'

  // --- Types ---
  interface ChatMsg {
    id: string
    role: string
    text: string
    ts: string
    seq?: number
    model?: string
    tier?: string
    cost_usd?: number
    duration_ms?: number
    skills?: string[]
    conv_id?: string
    media?: any[]
    reactions?: any[]
    content_blocks?: any[]
  }

  interface Tier {
    name: string
    model: string
  }

  // --- Single conversation state ---
  let convId = $state(localStorage.getItem('alf-chat-convid') || '')
  const clientId = Math.random().toString(36).slice(2, 10)

  function genId(): string {
    return Math.random().toString(36).slice(2, 10)
  }

  function saveConvId() {
    localStorage.setItem('alf-chat-convid', convId)
    // Notify server so other devices/tabs sync.
    api('/api/chat/active', {
      method: 'PUT',
      body: JSON.stringify({ conv_id: convId, client_id: clientId })
    }).catch(() => {})
  }

  // Load the active conversation from server on startup.
  async function loadActiveConversation() {
    try {
      const data = await api<any>('/api/chat/conversations')
      const allConvs = data.conversations || []
      const convs = allConvs.filter((c: any) => c.msg_count > 0)
      // Server's active_conv_id is the source of truth.
      const serverActive = data.active_conv_id
      if (serverActive && allConvs.some((c: any) => c.id === serverActive)) {
        convId = serverActive
      } else if (convs.length > 0) {
        convId = convs[convs.length - 1].id // most recent (ASC order)
      } else {
        // No conversations yet — create one.
        convId = genId()
        await api('/api/chat/conversations', { method: 'POST', body: JSON.stringify({ id: convId, title: 'Chat' }) }).catch(() => {})
      }
      saveConvId()
    } catch { /* server not ready yet */ }
  }

  // --- Messages ---
  let messages = $state<ChatMsg[]>([])
  let sending = $state(false)
  let tiers = $state<Tier[]>([])
  let messagesContainer: HTMLDivElement
  let selectedTier = $state(localStorage.getItem('alf-chat-tier') || '')
  let collapseBlocks = $state(localStorage.getItem('alf-chat-collapse') !== 'false')

  // Block visibility filter (#196)
  type ChatFilter = 'all' | 'clean' | 'thinking' | 'tools'
  let chatFilter = $state<ChatFilter>((localStorage.getItem('alf-chat-filter') as ChatFilter) || 'all')
  let hideThinking = $derived(chatFilter === 'clean' || chatFilter === 'tools')
  let hideTools = $derived(chatFilter === 'clean' || chatFilter === 'thinking')

  function onFilterChange(e: Event) {
    const detail = (e as CustomEvent).detail
    if (detail?.value) {
      chatFilter = detail.value as ChatFilter
      localStorage.setItem('alf-chat-filter', chatFilter)
    }
  }

  function bindFilterGroup(node: HTMLElement) {
    node.addEventListener('alf-change', onFilterChange)
    return { destroy() { node.removeEventListener('alf-change', onFilterChange) } }
  }
  let streamingBlocks = $state<any[]>([])
  let streamingText = $state('')
  let stoppedByUser = false
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let activeJobId = $state<string | null>(null)
  let messageQueue = $state<{ message: string; mediaFiles: MediaFile[]; model: string }[]>([])
  let abortController: AbortController | null = null
  let draft = $state('')
  let activeSkills = $state<string[]>([])

  async function loadActiveSkills() {
    try {
      const data = await api<{ skills: string[] }>('/api/chat/skills')
      activeSkills = data.skills || []
    } catch { /* silent */ }
  }

  async function dismissSkill(name: string) {
    try {
      await api(`/api/chat/skills?name=${encodeURIComponent(name)}`, { method: 'DELETE' })
      activeSkills = activeSkills.filter(s => s !== name)
    } catch { /* silent */ }
  }

  function updateDraft(text: string) {
    draft = text
  }

  // Send to agents modal
  let showAgentModal = $state(false)
  let agentModalPrompt = $state('')
  let agentModalTeam = $state('')
  let agentModalValidation = $state(false)
  let agentModalTeams = $state<{ name: string; description?: string }[]>([])
  let agentModalLaunching = $state(false)

  function openAgentModal(text: string) {
    agentModalPrompt = text
    agentModalTeam = ''
    agentModalValidation = false
    agentModalLaunching = false
    // Load teams
    api<{ teams: { name: string; description?: string }[] }>('/api/teams')
      .then(data => { agentModalTeams = data?.teams || [] })
      .catch(() => { agentModalTeams = [] })
    showAgentModal = true
  }

  async function launchAgentTask() {
    agentModalLaunching = true
    const message = agentModalTeam
      ? `[Use team: ${agentModalTeam}]\n${agentModalPrompt}`
      : agentModalPrompt
    try {
      await api('POST', '/api/tasks', { message, need_validation: agentModalValidation })
      toasts.show('Task launched')
      showAgentModal = false
    } catch {
      toasts.show('Failed to launch task', 'error')
    } finally {
      agentModalLaunching = false
    }
  }

  function sortMessages(msgs: ChatMsg[]): ChatMsg[] {
    return msgs.sort((a, b) => {
      // Primary: seq (guaranteed unique per conv). Fallback: timestamp.
      if (a.seq != null && b.seq != null && a.seq !== b.seq) return a.seq - b.seq
      return a.ts.localeCompare(b.ts)
    })
  }

  function setMessages(msgs: ChatMsg[]) {
    messages = sortMessages(msgs)
  }

  function appendMessage(msg: ChatMsg) {
    messages = sortMessages([...messages, msg])
  }

  async function scrollToBottom() {
    await tick()
    if (messagesContainer) {
      requestAnimationFrame(() => {
        messagesContainer.scrollTop = messagesContainer.scrollHeight
      })
    }
  }

  let loadingOlder = $state(false)
  let hasOlderMessages = $state(false)

  async function loadHistory() {
    if (!convId) {
      setMessages([])
      return
    }
    try {
      const data = await api<ChatMsg[]>(`/api/chat?limit=50&conv_id=${convId}`)
      setMessages(data || [])
      hasOlderMessages = (data?.length || 0) >= 50
      scrollToBottom()
    } catch {
      setMessages([])
    }
  }

  async function loadOlderMessages() {
    if (loadingOlder || !convId || messages.length === 0) return
    loadingOlder = true
    try {
      const oldest = messages[0]
      const data = await api<ChatMsg[]>(`/api/chat?limit=50&conv_id=${convId}&before=${oldest.ts}`)
      if (data && data.length > 0) {
        const container = messagesContainer
        const prevHeight = container?.scrollHeight || 0
        messages = sortMessages([...data, ...messages])
        await tick()
        if (container) {
          container.scrollTop = container.scrollHeight - prevHeight
        }
      }
      hasOlderMessages = (data?.length || 0) >= 50
    } catch { /* ignore */ }
    loadingOlder = false
  }

  function onMessagesScroll(e: Event) {
    const el = e.target as HTMLDivElement
    if (el.scrollTop < 100 && hasOlderMessages) {
      loadOlderMessages()
    }
  }

  async function loadTiers() {
    try {
      const data = await api<any>('/api/tiers')
      tiers = (data.tiers || []).map((t: any) => ({ name: t.name, model: t.model }))
    } catch {
      tiers = []
    }
  }

  // Check for active job on load (reconnect to stream)
  async function checkActiveJob() {
    if (sending || !convId) return
    try {
      const data = await api<any>(`/api/chat/job?conv_id=${convId}`)
      if (data.active && data.job_id) {
        activeJobId = data.job_id
        sending = true
        reconnectToStream(data.job_id, 0)
      }
    } catch { /* no active job */ }
  }

  interface MediaFile {
    upload_id: string
    file_name: string
    mime_type: string
  }

  // --- Send message ---
  async function handleSend(message: string, mediaFiles: MediaFile[], model: string) {
    draft = ''

    // Client-side command handling
    const trimmed = message.trim()
    if (trimmed === '/new') {
      await newConversation()
      return
    }
    if (trimmed === '/skills') {
      nav.navigateTo('skills')
      return
    }

    // Queue if already sending
    if (sending) {
      messageQueue = [...messageQueue, { message, mediaFiles, model }]
      return
    }

    await doSend(message, mediaFiles, model)
  }

  async function doSend(message: string, mediaFiles: MediaFile[], model: string) {
    sending = true
    stoppedByUser = false
    streamingBlocks = []
    streamingText = ''

    const mediaIds = mediaFiles.map(f => f.upload_id)

    // Add user message optimistically
    if (message || mediaFiles.length > 0) {
      const maxSeq = messages.reduce((max, m) => Math.max(max, m.seq ?? 0), 0)
      const userMsg: ChatMsg = {
        id: 'temp-' + Date.now(),
        role: 'user',
        text: message,
        ts: new Date().toISOString(),
        seq: maxSeq + 1,
        conv_id: convId,
        media: mediaFiles.map(f => ({ upload_id: f.upload_id, type: f.mime_type?.startsWith('image/') ? 'photo' : 'document', file_name: f.file_name, mime_type: f.mime_type })),
      }
      appendMessage(userMsg)
      scrollToBottom()
    }

    abortController = new AbortController()
    try {
      const body: any = { message, conv_id: convId }
      if (mediaIds.length > 0) body.media_ids = mediaIds
      if (model) body.model = model

      // POST /api/chat returns SSE stream
      const res = await fetch('/api/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Requested-With': 'XMLHttpRequest',
        },
        body: JSON.stringify(body),
        credentials: 'same-origin',
        signal: abortController.signal,
      })

      if (res.status === 401) {
        toasts.show('Session expired', 'error')
        sending = false
        return
      }

      if (!res.ok) {
        const err = await res.json().catch(() => ({ error: 'Send failed' }))
        toasts.show(err.error || 'Send failed', 'error')
        sending = false
        return
      }

      // Read SSE stream
      await readStream(res)
    } catch (e: any) {
      toasts.show(e.message || 'Send failed', 'error')
      sending = false
    }
  }

  async function readStream(res: Response) {
    const reader = res.body?.getReader()
    if (!reader) { sending = false; return }

    const decoder = new TextDecoder()
    let buffer = ''

    // Track content blocks as they stream
    let currentBlocks: any[] = []
    let currentText = ''
    let pendingEvent = '' // SSE event type from "event:" line

    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (line === '') {
            // Empty line = end of SSE message, reset pending event
            pendingEvent = ''
            continue
          }

          if (line.startsWith('event: ')) {
            pendingEvent = line.slice(7).trim()
            continue
          }

          if (!line.startsWith('data: ')) continue
          const dataStr = line.slice(6)
          let data: any
          try { data = JSON.parse(dataStr) } catch { continue }

          // Use the paired event type, or fall back to data.type
          const eventType = pendingEvent || data.type || ''

          if (eventType === 'job') {
            activeJobId = data.job_id
            continue
          }

          if (eventType === 'error') {
            // Show toast for immediate feedback; the server also emits a
            // "system" event with a classified message that persists in chat.
            toasts.show(data.error || data.text || 'Stream error', 'error')
            continue
          }

          if (eventType === 'system') {
            if (data.text) {
              appendMessage({
                id: 'sys-' + Date.now(),
                role: 'system',
                text: data.text,
                ts: new Date().toISOString(),
              })
            }
            continue
          }

          if (eventType === 'done') {
            // Apply metadata from done event to the last assistant message
            const lastAssistant = [...messages].reverse().find(m => m.role === 'assistant')
            if (lastAssistant && data) {
              if (data.model) lastAssistant.model = data.model
              if (data.tier) lastAssistant.tier = data.tier
              if (data.cost_usd) lastAssistant.cost_usd = data.cost_usd
              if (data.duration_ms) lastAssistant.duration_ms = data.duration_ms
              if (data.skills) lastAssistant.skills = data.skills
              messages = [...messages]
            }
            loadActiveSkills()
            continue
          }

          // Inject the event type into data for the handler
          data.type = data.type || eventType
          handleStreamEvent(data, currentBlocks, (updated) => {
            currentBlocks = updated
          }, (text) => {
            currentText = text
          })
        }

        // Update streaming display
        streamingBlocks = [...currentBlocks]
        streamingText = currentText
        scrollToBottom()
      }
    } catch {
      // Stream ended or errored
    } finally {
      abortController = null

      // stopCall() already cleaned up UI state — skip the rest to avoid
      // conflicting state updates and double loadHistory() calls.
      if (stoppedByUser) return

      const finalText = streamingText
      sending = false
      activeJobId = null
      streamingBlocks = []
      streamingText = ''
      // Small delay to ensure server has committed the message before we fetch.
      await new Promise(r => setTimeout(r, 100))
      await loadHistory()
      scrollToBottom()

      // Notifications
      if (document.hidden && 'Notification' in window && Notification.permission === 'granted' && finalText) {
        new Notification('ALF', { body: finalText.slice(0, 100) })
      }
      if (finalText) sound.play()
      if (nav.currentView !== 'chat') {
        nav.incrementBadge('chat')
      }

      // Process queue
      if (messageQueue.length > 0) {
        const next = messageQueue[0]
        messageQueue = messageQueue.slice(1)
        doSend(next.message, next.mediaFiles, next.model)
      }
    }
  }

  // SSE event parsing — the server uses paired event:/data: lines
  // We need to handle a stateful parse

  async function reconnectToStream(jobId: string, offset: number) {
    try {
      const res = await fetch(`/api/chat/job?stream=${jobId}&offset=${offset}`, {
        credentials: 'same-origin',
        headers: { 'X-Requested-With': 'XMLHttpRequest' },
      })
      if (!res.ok) {
        sending = false
        return
      }
      await readStream(res)
    } catch {
      sending = false
    }
  }

  function handleStreamEvent(
    data: any,
    blocks: any[],
    setBlocks: (b: any[]) => void,
    setText: (t: string) => void,
  ) {
    // The SSE stream sends events like:
    // event: content_block_start → data: { type, index, content_block: { type, ... } }
    // event: content_block_delta → data: { type, index, delta: { type, text } }
    // event: content_block_stop → data: { type, index }
    // event: message_stop → data: { ... done data }
    // event: error → data: { error: "..." }

    const type = data.type

    if (type === 'content_block_start') {
      const block = data.content_block || {}
      blocks.push({
        type: block.type || 'text',
        text: '',
        name: block.name || '',
        input: block.input || null,
        thinking: '',
      })
      setBlocks([...blocks])
    } else if (type === 'content_block_delta') {
      const idx = data.index ?? (blocks.length - 1)
      if (idx >= 0 && idx < blocks.length) {
        const delta = data.delta || {}
        if (delta.type === 'text_delta' && delta.text) {
          blocks[idx].text = (blocks[idx].text || '') + delta.text
        } else if (delta.type === 'thinking_delta' && delta.thinking) {
          blocks[idx].thinking = (blocks[idx].thinking || '') + delta.thinking
        } else if (delta.type === 'input_json_delta' && delta.partial_json) {
          const prev = typeof blocks[idx].input === 'string' ? blocks[idx].input : ''
          blocks[idx].input = prev + delta.partial_json
        }
        setBlocks([...blocks])
      }
    } else if (type === 'content_block_stop') {
      // Try to parse JSON input for tool_use blocks
      const idx = data.index ?? (blocks.length - 1)
      if (idx >= 0 && idx < blocks.length && blocks[idx].type === 'tool_use') {
        try {
          if (typeof blocks[idx].input === 'string') {
            blocks[idx].input = JSON.parse(blocks[idx].input)
          }
        } catch { /* keep as string */ }
      }
      setBlocks([...blocks])
    } else if (type === 'text_delta') {
      // Normalized event from comms pipeline (Codex, CLI, etc.)
      const delta = data.text || data.data?.text || ''
      if (delta) {
        if (blocks.length === 0 || blocks[blocks.length - 1].type !== 'text') {
          blocks.push({ type: 'text', text: '', thinking: '' })
        }
        blocks[blocks.length - 1].text += delta
        setBlocks([...blocks])
      }
    } else if (type === 'text') {
      // Final text event from comms pipeline — only use it if no blocks
      // were built during streaming (text_delta events already populated
      // text blocks). Adding it when blocks exist would duplicate content
      // and may expose raw tool-call XML (#124).
      const text = data.text || data.data?.text || ''
      if (text && blocks.length === 0) {
        blocks.push({ type: 'text', text: '', thinking: '' })
        blocks[blocks.length - 1].text = text
        setBlocks([...blocks])
      }
    } else if (type === 'thinking') {
      // Normalized thinking event from comms pipeline
      const text = data.text || data.data?.text || ''
      if (text) {
        if (blocks.length === 0 || blocks[blocks.length - 1].type !== 'thinking') {
          blocks.push({ type: 'thinking', text: '', thinking: '' })
        }
        blocks[blocks.length - 1].thinking += text
        setBlocks([...blocks])
      }
    } else if (type === 'tool_use') {
      // Normalized tool_use start from comms pipeline
      blocks.push({ type: 'tool_use', text: '', name: data.name || '', input: '', thinking: '' })
      setBlocks([...blocks])
    } else if (type === 'tool_input') {
      // Normalized tool input chunk from comms pipeline
      const last = blocks[blocks.length - 1]
      if (last && last.type === 'tool_use') {
        last.input = (typeof last.input === 'string' ? last.input : '') + (data.chunk || '')
        setBlocks([...blocks])
      }
    } else if (type === 'tool_result') {
      // Normalized tool result from comms pipeline
      const last = blocks[blocks.length - 1]
      if (last && last.type === 'tool_use') {
        try {
          if (typeof last.input === 'string') last.input = JSON.parse(last.input)
        } catch { /* keep as string */ }
      }
      setBlocks([...blocks])
    } else if (type === 'message_delta' || type === 'message_stop') {
      // Done event — may contain cost, model, etc.
      // No action needed; stream will end and we reload history
    } else if (type === 'error' || data.error) {
      toasts.show(data.error || data.text || 'Stream error', 'error')
    }

    // Build aggregate text for simple display
    const fullText = blocks
      .filter(b => b.type === 'text')
      .map(b => b.text)
      .join('')
    setText(fullText)
  }

  // --- Stop active call (instant) ---
  function stopCall() {
    stoppedByUser = true
    messageQueue = [] // clear pending queue
    // Abort the active fetch stream immediately
    abortController?.abort()
    abortController = null
    // Reset UI state instantly — no awaits before this
    sending = false
    activeJobId = null
    streamingBlocks = []
    streamingText = ''
    // Cancel backend job (persists "cancelled" system message), then reload history
    api('DELETE', `/api/chat/job?conv_id=${encodeURIComponent(convId)}`)
      .catch(() => {})
      .finally(() => loadHistory().then(() => scrollToBottom()))
  }

  // --- New conversation ---
  async function newConversation() {
    try {
      // Reset LLM session state.
      await api<any>('/api/chat', { method: 'DELETE' })
      // Fresh convId.
      convId = genId()
      await api('/api/chat/conversations', { method: 'POST', body: JSON.stringify({ id: convId, title: 'Chat' }) }).catch(() => {})
      saveConvId()
      setMessages([])
      toasts.show('New conversation started', 'success')
    } catch (e: any) {
      toasts.show(e.error || 'Failed to start new conversation', 'error')
    }
  }

  // Scroll to bottom when navigating back to chat view
  $effect(() => {
    if (nav.currentView === 'chat') {
      scrollToBottom()
    }
  })

  // --- Lifecycle ---
  let unsubTiers: (() => void) | null = null
  let unsubNewMsg: (() => void) | null = null
  let unsubActiveConv: (() => void) | null = null

  onMount(async () => {
    // Request notification permission
    if ('Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission()
    }
    // Load the active conversation from server.
    await loadActiveConversation()
    await loadTiers()
    loadActiveSkills()
    unsubTiers = events.subscribe('tiers', () => loadTiers())
    unsubActiveConv = events.subscribe('active_conv', (data) => {
      try {
        const parsed = JSON.parse(data || '{}')
        if (parsed.client_id === clientId) return // echo suppression
        if (parsed.conv_id && parsed.conv_id !== convId) {
          convId = parsed.conv_id
          localStorage.setItem('alf-chat-convid', convId)
          loadHistory().then(() => scrollToBottom())
        }
      } catch {}
    })
    unsubNewMsg = events.subscribe('new_message', () => {
      if (!convId) return
      // Fetch latest messages and append any new ones (safe during streaming).
      api<ChatMsg[]>(`/api/chat?limit=5&conv_id=${convId}`).then(recent => {
        if (!recent?.length) return
        const existingIds = new Set(messages.map(m => m.id))
        // Only append assistant messages — user messages are already in the UI
        // via optimistic insert (with temp- IDs that won't match server IDs).
        const newMsgs = recent.filter(m => !existingIds.has(m.id) && m.role === 'assistant')
        if (newMsgs.length > 0) {
          messages = sortMessages([...messages, ...newMsgs])
          scrollToBottom()
        }
      }).catch(() => {})
    })
    if (convId) {
      await loadHistory()
    }
    await checkActiveJob()

    // Poll every 2s: sync active conv + fetch new messages from other devices.
    pollTimer = setInterval(() => {
      if (!convId) return
      // 1. Sync active conversation from server.
      api<any>('/api/chat/active').then(data => {
        const serverConv = data?.active_conv_id
        if (serverConv && serverConv !== convId) {
          convId = serverConv
          localStorage.setItem('alf-chat-convid', convId)
          loadHistory().then(() => scrollToBottom())
          return // skip message poll — loadHistory covers it
        }
      }).catch(() => {})
      // 2. Fetch latest messages and merge new ones.
      if (sending) return // don't interfere with active stream
      api<ChatMsg[]>(`/api/chat?limit=5&conv_id=${convId}`).then(recent => {
        if (!recent?.length) return
        const existingIds = new Set(messages.map(m => m.id))
        const newMsgs = recent.filter(m => !existingIds.has(m.id))
        if (newMsgs.length > 0) {
          messages = sortMessages([...messages, ...newMsgs])
          scrollToBottom()
        }
      }).catch(() => {})
    }, 2000)
  })

  onDestroy(() => {
    if (pollTimer) clearInterval(pollTimer)
    unsubTiers?.()
    unsubNewMsg?.()
    unsubActiveConv?.()
  })
</script>

<div class="chat-view">
  <!-- Header -->
  <div class="chat-header">
    <button class="btn btn-ghost btn-sm" onclick={newConversation} title="New conversation">
      <RotateCw size={14} />
      New
    </button>
    <div class="chat-header-spacer"></div>
    <div use:bindFilterGroup>
      <alf-btn-group value={chatFilter}>
        <button class="btn btn-sm" data-value="all">All</button>
        <button class="btn btn-sm" data-value="clean">Clean</button>
        <button class="btn btn-sm" data-value="thinking">Thinking</button>
        <button class="btn btn-sm" data-value="tools">Tools</button>
      </alf-btn-group>
    </div>
  </div>

  <!-- Messages -->
  <div class="chat-messages" bind:this={messagesContainer} onscroll={onMessagesScroll}>
    {#if loadingOlder}
      <div class="loading-older">Loading older messages...</div>
    {/if}

    {#if messages.length === 0 && !sending}
      <div class="chat-empty">
        <MessageCircle size={32} />
        <p>No messages yet. Start a conversation.</p>
      </div>
    {/if}

    {#each messages as msg (msg.id)}
      {#if msg.role === 'assistant' && msg.content_blocks && msg.content_blocks.length > 1}
        {@const visibleBlocks = msg.content_blocks.filter(b => {
          if (b.type === 'thinking' && hideThinking) return false
          if ((b.type === 'tool_use' || b.type === 'tool_result') && hideTools) return false
          if (b.type === 'text') return b.text && b.text.trim()
          if (b.type === 'tool_result') return (b.content || b.text || '').trim()
          return true
        })}
        {#each visibleBlocks as block, bi (bi)}
          {@const isLast = bi === visibleBlocks.length - 1}
          <ChatMessageComponent
            msg={{
              ...msg,
              id: `${msg.id}-block-${bi}`,
              text: block.type === 'text' ? (block.text || '') : '',
              content_blocks: [block],
              // Only show footer metadata on the last block
              model: isLast ? msg.model : undefined,
              tier: isLast ? msg.tier : undefined,
              cost_usd: isLast ? msg.cost_usd : undefined,
              duration_ms: isLast ? msg.duration_ms : undefined,
              skills: isLast ? msg.skills : undefined,
              reactions: isLast ? msg.reactions : undefined,
            }}
            {convId}
            {collapseBlocks} {hideThinking} {hideTools}
            onSendToTask={isLast ? openAgentModal : undefined}
          />
        {/each}
      {:else}
        <ChatMessageComponent {msg} {convId} {collapseBlocks} {hideThinking} {hideTools} onSendToTask={openAgentModal} />
      {/if}
    {/each}

    <!-- Streaming response — each block rendered as its own bubble (#127) -->
    {#if sending && streamingBlocks.length > 0}
      {#each streamingBlocks as block, i (i)}
        <ChatMessageComponent
          msg={{
            id: `streaming-${i}`,
            role: 'assistant',
            text: block.type === 'text' ? (block.text || '') : '',
            ts: new Date().toISOString(),
            content_blocks: [block],
          }}
          {convId}
          {collapseBlocks} {hideThinking} {hideTools}
        />
      {/each}
    {:else if sending}
      <div class="chat-msg chat-msg-assistant typing-indicator">
        <span class="dot"></span>
        <span class="dot"></span>
        <span class="dot"></span>
      </div>
    {/if}

    <!-- Queued messages -->
    {#each messageQueue as queued, i}
      <div class="chat-msg chat-msg-user queued-msg">
        <div class="msg-text">{queued.message}</div>
        <div class="queued-footer">
          <span class="queued-badge">queued #{i + 1}</span>
          <button class="queued-cancel" onclick={() => { messageQueue = messageQueue.filter((_, idx) => idx !== i) }} title="Cancel queued message">
            <X size={12} /> cancel
          </button>
        </div>
      </div>
    {/each}
  </div>

  <!-- Input -->
  <ChatInput onSend={handleSend} onStop={stopCall} {sending} {tiers} {draft} onDraftChange={updateDraft} selectedModel={selectedTier} onModelChange={(m) => { selectedTier = m; localStorage.setItem('alf-chat-tier', m) }} {activeSkills} onDismissSkill={dismissSkill} />
</div>

<!-- Send to Agents Modal -->
<Modal open={showAgentModal} onclose={() => showAgentModal = false}>
  <h3 style="margin:0 0 12px">Send to Agents</h3>
  <div class="agent-modal-form">
    <div class="form-group">
      <label for="agent-prompt">Prompt</label>
      <textarea id="agent-prompt" class="input" rows="4" bind:value={agentModalPrompt}></textarea>
    </div>
    <div class="form-group">
      <label for="agent-team">Team</label>
      <select id="agent-team" class="input" bind:value={agentModalTeam}>
        <option value="">Auto (best fit)</option>
        {#each agentModalTeams as team}
          <option value={team.name}>{team.name}{team.description ? ` - ${team.description}` : ''}</option>
        {/each}
      </select>
    </div>
    <div class="form-group">
      <Toggle bind:checked={agentModalValidation} label="Review plan before execution" />
    </div>
    <div class="agent-modal-actions">
      <button class="btn btn-secondary" onclick={() => showAgentModal = false}>Cancel</button>
      <button class="btn btn-primary" onclick={launchAgentTask} disabled={agentModalLaunching}>
        <Play size={14} /> {agentModalLaunching ? 'Launching...' : 'Launch'}
      </button>
    </div>
  </div>
</Modal>

<style>
  .chat-view {
    display: flex;
    flex-direction: column;
    height: calc(100vh - 48px);
    max-height: calc(100vh - 48px);
    margin-bottom: -24px;
  }

  /* Header */
  .chat-header {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }

  .chat-header-spacer {
    flex: 1;
  }

  /* Messages */
  .chat-messages {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .chat-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    color: var(--text-dim);
    gap: 12px;
    padding: 40px;
  }

  .chat-empty p {
    font-size: var(--font-sm, 13px);
  }

  .loading-older {
    text-align: center;
    padding: 8px;
    color: var(--text-dim);
    font-size: var(--font-sm, 13px);
  }

  /* Typing indicator */
  .typing-indicator {
    display: flex;
    gap: 4px;
    padding: 12px 16px;
    align-self: flex-start;
    background: var(--bg-card);
    border: none;
    border-radius: 12px;
    border-bottom-left-radius: 4px;
  }

  .dot {
    width: 6px;
    height: 6px;
    background: var(--text-dim);
    border-radius: 50%;
    animation: bounce 1.4s infinite ease both;
  }

  .dot:nth-child(1) { animation-delay: -0.32s; }
  .dot:nth-child(2) { animation-delay: -0.16s; }

  @keyframes bounce {
    0%, 80%, 100% { transform: scale(0.6); opacity: 0.4; }
    40% { transform: scale(1); opacity: 1; }
  }

  /* Queued messages */
  .queued-msg {
    opacity: 0.7;
  }

  .queued-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 6px;
    padding-top: 4px;
    border-top: 1px solid color-mix(in srgb, var(--text) 15%, transparent);
    gap: 8px;
  }

  .queued-badge {
    font-size: var(--font-xs, 11px);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    opacity: 0.7;
  }

  .queued-cancel {
    display: flex;
    align-items: center;
    gap: 3px;
    background: none;
    border: none;
    color: inherit;
    opacity: 0.5;
    cursor: pointer;
    padding: 2px 6px;
    border-radius: 4px;
    font-size: var(--font-xs, 11px);
    transition: opacity 0.15s, background 0.15s;
  }

  .queued-cancel:hover {
    opacity: 1;
    background: color-mix(in srgb, var(--text) 15%, transparent);
  }

  /* Agent modal */
  .agent-modal-form {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .agent-modal-form .form-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .agent-modal-form label {
    font-size: var(--font-sm, 13px);
    font-weight: 500;
  }

  .agent-modal-form textarea,
  .agent-modal-form select {
    width: 100%;
    box-sizing: border-box;
  }

  .agent-modal-form .checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
  }

  .agent-modal-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 4px;
  }

  @media (max-width: 768px) {
    .chat-view {
      height: calc(100dvh - 36px - env(safe-area-inset-top, 0px));
      max-height: calc(100dvh - 36px - env(safe-area-inset-top, 0px));
      margin-bottom: 0;
    }

    .chat-messages {
      padding: 8px;
    }
  }
</style>
