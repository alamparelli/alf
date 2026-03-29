<script lang="ts">
  import { onMount } from 'svelte'
  import { theme } from '../stores/theme.svelte'
  import { nav } from '../stores/nav.svelte'
  import { toasts } from '../stores/toast.svelte'
  import Modal from './shared/Modal.svelte'

  let { slug }: { slug: string } = $props()
  let iframe: HTMLIFrameElement

  // Sheet state
  let sheetOpen = $state(false)
  let sheetHtml = $state('')
  let sheetHasActions = $state(false)

  // Confirm dialog state
  let confirmOpen = $state(false)
  let confirmMsg = $state('')
  let confirmTitle = $state('')
  let confirmOk = $state('OK')
  let confirmCancel = $state('Cancel')
  let confirmReplyId = $state(0)

  // Prompt dialog state
  let promptOpen = $state(false)
  let promptMsg = $state('')
  let promptTitle = $state('')
  let promptValue = $state('')
  let promptPlaceholder = $state('')
  let promptOk = $state('OK')
  let promptReplyId = $state(0)

  function reply(replyId: number, result: any, error?: string) {
    iframe?.contentWindow?.postMessage(
      { type: 'alf', action: 'reply', _replyId: replyId, result, error },
      '*'
    )
  }

  /** Inject mobile-responsive sheet CSS into iframe */
  function injectSheetCSS(frame: HTMLIFrameElement) {
    try {
      const doc = frame.contentDocument
      if (!doc) return
      if (doc.getElementById('alf-sheet-css')) return
      const style = doc.createElement('style')
      style.id = 'alf-sheet-css'
      style.textContent = `
@media (max-width: 768px) {
  dialog, dialog[open], .modal, [role="dialog"], .popup, .overlay-modal {
    all: revert;
    position: fixed !important;
    bottom: 0 !important;
    left: 0 !important;
    right: 0 !important;
    top: auto !important;
    width: 100% !important;
    max-width: 100% !important;
    max-height: 85vh !important;
    margin: 0 !important;
    border-radius: 16px 16px 0 0 !important;
    padding: 0 1rem calc(1rem + env(safe-area-inset-bottom, 0px)) !important;
    background: var(--bg-card, #1c1c1c) !important;
    color: var(--text, #e0e0e0) !important;
    border: none !important;
    box-shadow: 0 -4px 32px rgba(0,0,0,0.3) !important;
    overflow-y: auto !important;
    animation: alf-sheet-up 0.25s ease !important;
    z-index: 9999 !important;
    display: flex;
    flex-direction: column;
  }
  dialog::backdrop, .modal-backdrop, .overlay-backdrop {
    background: rgba(0,0,0,0.5) !important;
  }
  dialog::before, .modal::before, [role="dialog"]::before {
    content: '';
    display: block;
    width: 36px;
    height: 5px;
    background: currentColor;
    opacity: 0.2;
    border-radius: 3px;
    margin: 10px auto 12px;
    flex-shrink: 0;
  }
  @keyframes alf-sheet-up {
    from { transform: translateY(100%); }
    to { transform: translateY(0); }
  }
}
`
      doc.head.appendChild(style)
    } catch {
      // cross-origin — skip
    }
  }

  function handleMessage(e: MessageEvent) {
    if (!e.data || e.data.type !== 'alf-app') return
    if (e.source !== iframe?.contentWindow) return

    const { action, _replyId } = e.data

    switch (action) {
      // ── Sheet ──
      case 'sheet':
        sheetHtml = e.data.html || ''
        sheetHasActions = !!e.data.hasActions
        sheetOpen = true
        break
      case 'update-sheet':
        sheetHtml = e.data.html || ''
        break
      case 'close-sheet':
        sheetOpen = false
        sheetHtml = ''
        sheetHasActions = false
        break

      // ── Navigate ──
      case 'navigate':
        nav.navigateTo(e.data.view)
        break

      // ── Toast ──
      case 'toast':
        toasts.show(e.data.msg, e.data.type)
        break

      // ── Confirm ──
      case 'confirm':
        confirmMsg = e.data.message || ''
        confirmTitle = e.data.title || ''
        confirmOk = e.data.confirmText || 'OK'
        confirmCancel = e.data.cancelText || 'Cancel'
        confirmReplyId = _replyId || 0
        confirmOpen = true
        break

      // ── Prompt ──
      case 'prompt':
        promptMsg = e.data.message || ''
        promptTitle = e.data.title || ''
        promptValue = e.data.defaultValue || ''
        promptPlaceholder = e.data.placeholder || ''
        promptOk = e.data.confirmText || 'OK'
        promptReplyId = _replyId || 0
        promptOpen = true
        break

      // ── Clipboard ──
      case 'clipboard-write':
        navigator.clipboard.writeText(e.data.text || '')
          .then(() => reply(_replyId, true))
          .catch(err => reply(_replyId, null, err.message))
        break
      case 'clipboard-read':
        navigator.clipboard.readText()
          .then(text => reply(_replyId, text))
          .catch(err => reply(_replyId, null, err.message))
        break

      // ── Inter-app events ──
      case 'event-emit':
        // Broadcast to all other app iframes
        document.querySelectorAll<HTMLIFrameElement>('iframe.page-frame').forEach(f => {
          if (f !== iframe && f.contentWindow) {
            f.contentWindow.postMessage(
              { type: 'alf', action: 'event-relay', event: e.data.event, payload: e.data.payload },
              '*'
            )
          }
        })
        break

      // ── Badge ──
      case 'badge-set':
        nav.setBadge('page:' + (e.data.slug || slug), e.data.count || 0)
        break
      case 'badge-increment':
        nav.incrementBadge('page:' + (e.data.slug || slug))
        break
    }
  }

  /** Delegated click handler for data-action elements inside sheets */
  function handleSheetClick(e: MouseEvent) {
    if (!sheetHasActions) return
    const target = (e.target as HTMLElement).closest('[data-action]') as HTMLElement
    if (!target) return
    const actionName = target.dataset.action
    if (!actionName) return
    // Collect all data-* attributes as params (excluding data-action itself)
    const params: Record<string, string> = {}
    for (const key of Object.keys(target.dataset)) {
      if (key !== 'action') params[key] = target.dataset[key]!
    }
    // Relay to iframe
    iframe?.contentWindow?.postMessage(
      { type: 'alf', action: 'sheet-action', name: actionName, params },
      '*'
    )
  }

  function handleConfirm(result: boolean) {
    confirmOpen = false
    if (confirmReplyId) reply(confirmReplyId, result)
  }

  function handlePromptSubmit() {
    promptOpen = false
    if (promptReplyId) reply(promptReplyId, promptValue)
  }

  function handlePromptCancel() {
    promptOpen = false
    if (promptReplyId) reply(promptReplyId, null)
  }

  onMount(() => {
    ;(window as any).navigateTo = (view: string) => nav.navigateTo(view)

    window.addEventListener('message', handleMessage)

    if (iframe) {
      iframe.addEventListener('load', () => {
        theme.syncIframe(iframe)
        injectSheetCSS(iframe)
      })
    }

    return () => window.removeEventListener('message', handleMessage)
  })

  $effect(() => {
    if (iframe) {
      theme.palette
      theme.syncIframe(iframe)
    }
  })
</script>

<iframe
  bind:this={iframe}
  class="page-frame"
  src={`/apps/${slug}/`}
  title={slug}
></iframe>

<!-- Sheet modal -->
<Modal open={sheetOpen} onclose={() => { sheetOpen = false; sheetHtml = ''; sheetHasActions = false }}>
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="sdk-sheet-content" onclick={handleSheetClick}>
    {@html sheetHtml}
  </div>
</Modal>

<!-- Confirm dialog -->
<Modal open={confirmOpen} onclose={() => handleConfirm(false)}>
  <div class="sdk-dialog">
    {#if confirmTitle}<h3>{confirmTitle}</h3>{/if}
    <p>{confirmMsg}</p>
    <div class="sdk-dialog-actions">
      <button class="btn-secondary" onclick={() => handleConfirm(false)}>{confirmCancel}</button>
      <button class="btn-primary" onclick={() => handleConfirm(true)}>{confirmOk}</button>
    </div>
  </div>
</Modal>

<!-- Prompt dialog -->
<Modal open={promptOpen} onclose={handlePromptCancel}>
  <div class="sdk-dialog">
    {#if promptTitle}<h3>{promptTitle}</h3>{/if}
    <p>{promptMsg}</p>
    <input
      type="text"
      class="sdk-input"
      bind:value={promptValue}
      placeholder={promptPlaceholder}
      onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') handlePromptSubmit() }}
    />
    <div class="sdk-dialog-actions">
      <button class="btn-secondary" onclick={handlePromptCancel}>Cancel</button>
      <button class="btn-primary" onclick={handlePromptSubmit}>{promptOk}</button>
    </div>
  </div>
</Modal>

<style>
  .page-frame {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    border: none;
  }

  @media (max-width: 768px) {
    .page-frame {
      top: env(safe-area-inset-top, 0px);
      height: calc(100% - env(safe-area-inset-top, 0px) - env(safe-area-inset-bottom, 0px));
    }
  }

  .sdk-dialog {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .sdk-dialog h3 {
    margin: 0;
    font-size: 1.1rem;
    font-weight: 600;
  }

  .sdk-dialog p {
    margin: 0;
    color: var(--text-muted);
    line-height: 1.5;
  }

  .sdk-input {
    width: 100%;
    padding: 8px 12px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--bg-input);
    color: var(--text);
    font-size: 0.9rem;
    outline: none;
  }

  .sdk-input:focus {
    border-color: var(--accent);
  }

  .sdk-dialog-actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    margin-top: 4px;
  }

  .btn-primary, .btn-secondary {
    padding: 8px 16px;
    border-radius: 6px;
    font-size: 0.85rem;
    cursor: pointer;
    border: none;
    font-weight: 500;
  }

  .btn-primary {
    background: var(--accent);
    color: var(--bg);
  }

  .btn-secondary {
    background: var(--bg-input);
    color: var(--text);
    border: 1px solid var(--border);
  }

  .btn-primary:hover { opacity: 0.9; }
  .btn-secondary:hover { background: var(--border); }
</style>
