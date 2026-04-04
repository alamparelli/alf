<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { theme } from '../stores/theme.svelte'
  import { nav } from '../stores/nav.svelte'
  import { toasts } from '../stores/toast.svelte'
  import Modal from './shared/Modal.svelte'

  let { slug, query = '' }: { slug: string; query?: string } = $props()
  let iframe: HTMLIFrameElement

  // Sheet state
  let iframeReady = $state(false)

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
  let promptMultiline = $state(false)
  let promptReplyId = $state(0)

  // App permissions (null = all allowed, string[] = restricted)
  let appPermissions: string[] | null = $state(null)

  function postToIframe(action: string) {
    iframe?.contentWindow?.postMessage({ type: 'alf', action }, location.origin)
  }

  function onVisibilityChange() {
    postToIframe(document.hidden ? 'hidden' : 'visible')
  }

  function reply(replyId: number, result: any, error?: string) {
    iframe?.contentWindow?.postMessage(
      { type: 'alf', action: 'reply', _replyId: replyId, result, error },
      location.origin
    )
  }

  /** Inject alf-ui.css design system into iframe (fallback — server already
   *  injects it into app HTML, so this is a no-op in most cases). */
  function injectUICSS(frame: HTMLIFrameElement) {
    try {
      const doc = frame.contentDocument
      if (!doc) return
      // Skip if already present (server-injected or prior call).
      if (doc.getElementById('alf-ui-css') ||
          doc.querySelector('link[href*="alf-ui.css"]')) return
      const link = doc.createElement('link')
      link.id = 'alf-ui-css'
      link.rel = 'stylesheet'
      link.href = '/static/alf-ui.css'
      doc.head.appendChild(link)
    } catch {
      // cross-origin — skip
    }
  }

  /** Inject safe area insets as CSS variables (iframes can't read env() from parent) */
  function injectSafeAreas(frame: HTMLIFrameElement) {
    try {
      const doc = frame.contentDocument
      if (!doc) return
      if (doc.getElementById('alf-safe-areas')) return
      // Read computed safe area values from the parent document
      const cs = getComputedStyle(document.documentElement)
      const top = cs.getPropertyValue('--sat').trim() || getComputedStyle(document.body).paddingTop || '0px'
      // Use a test element to resolve env() values
      const test = document.createElement('div')
      test.style.cssText = 'position:fixed;visibility:hidden;padding-top:env(safe-area-inset-top,0px);padding-bottom:env(safe-area-inset-bottom,0px);padding-left:env(safe-area-inset-left,0px);padding-right:env(safe-area-inset-right,0px);'
      document.body.appendChild(test)
      const computed = getComputedStyle(test)
      const sat = computed.paddingTop
      const sab = computed.paddingBottom
      const sal = computed.paddingLeft
      const sar = computed.paddingRight
      document.body.removeChild(test)

      const style = doc.createElement('style')
      style.id = 'alf-safe-areas'
      // Top safe area is already handled by the parent frame positioning
      // (top: 36px + env(safe-area-inset-top)). Override --page-padding-top
      // and --safe-area-top to 0 inside iframes to prevent double spacing.
      // env(safe-area-inset-top) cannot be overridden, so we override the
      // CSS variables that alf-ui.css uses instead.
      style.textContent = `
:root {
  --safe-area-top: 0px;
  --safe-area-bottom: ${sab};
  --safe-area-left: ${sal};
  --safe-area-right: ${sar};
  --page-padding-top: 1rem;
}
body {
  padding: 0 var(--safe-area-right) var(--safe-area-bottom) var(--safe-area-left);
  overflow-x: hidden;
}
`
      doc.head.appendChild(style)
    } catch {
      // cross-origin — skip
    }
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
    animation: alf-sheet-up 0.2s ease !important;
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

  // SEC-001 + SEC-P02: Sanitize HTML from iframe apps to prevent XSS in parent context.
  // Allowlist approach: only permit known-safe tags and attributes.
  // Double-parse to defeat mutation XSS (mXSS) where first parse creates benign DOM
  // but serialization + reparse creates dangerous elements.
  const SAFE_TAGS = new Set([
    'h1','h2','h3','h4','h5','h6','p','div','span','br','hr','pre','code','blockquote',
    'ul','ol','li','dl','dt','dd','table','thead','tbody','tfoot','tr','th','td','caption',
    'a','img','strong','em','b','i','u','s','small','sub','sup','mark','abbr',
    'button','input','select','option','textarea','label','fieldset','legend',
    'details','summary','figure','figcaption','time','svg','path','circle','rect','line',
    'g','text','defs','use','symbol','polyline','polygon','ellipse'
  ])
  const SAFE_ATTRS = new Set([
    'class','id','style','href','src','alt','title','width','height','colspan','rowspan',
    'type','name','value','placeholder','checked','disabled','readonly','rows','cols',
    'target','rel','role','aria-label','aria-hidden','aria-expanded','for',
    'data-action','data-id','data-value','data-field','data-type',
    'viewBox','d','fill','stroke','stroke-width','cx','cy','r','x','y','x1','y1','x2','y2',
    'points','transform','xmlns','stroke-linecap','stroke-linejoin'
  ])
  const DATA_ATTR_PREFIX = 'data-'

  function sanitizeHtml(html: string): string {
    // First pass: parse and strip
    const div = document.createElement('div')
    div.innerHTML = html

    function cleanNode(parent: Element) {
      for (const node of Array.from(parent.childNodes)) {
        if (node.nodeType === Node.TEXT_NODE) continue
        if (node.nodeType !== Node.ELEMENT_NODE) { node.remove(); continue }

        const el = node as Element
        const tag = el.tagName.toLowerCase()

        if (!SAFE_TAGS.has(tag)) {
          el.remove()
          continue
        }

        // Strip unsafe attributes
        for (const attr of Array.from(el.attributes)) {
          const name = attr.name.toLowerCase()
          if (!SAFE_ATTRS.has(name) && !name.startsWith(DATA_ATTR_PREFIX)) {
            el.removeAttribute(attr.name)
            continue
          }
          // Block javascript: URIs in href/src
          if ((name === 'href' || name === 'src') &&
              attr.value.replace(/[\s\x00-\x1f]/g, '').toLowerCase().startsWith('javascript:')) {
            el.removeAttribute(attr.name)
          }
        }

        if (el.children.length > 0) cleanNode(el)
      }
    }

    cleanNode(div)

    // Second pass: re-parse serialized output to defeat mXSS
    const div2 = document.createElement('div')
    div2.innerHTML = div.innerHTML
    cleanNode(div2)

    return div2.innerHTML
  }

  function handleMessage(e: MessageEvent) {
    if (!e.data || e.data.type !== 'alf-app') return
    if (e.source !== iframe?.contentWindow) return

    const { action, _replyId } = e.data

    switch (action) {
      // ── Sheet ──
      case 'sheet':
        sheetHtml = sanitizeHtml(e.data.html || '')
        sheetHasActions = !!e.data.hasActions
        sheetOpen = true
        break
      case 'update-sheet':
        sheetHtml = sanitizeHtml(e.data.html || '')
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
        promptMultiline = !!e.data.multiline
        promptReplyId = _replyId || 0
        promptOpen = true
        break

      // ── Clipboard (SEC-002: parent-side permission check) ──
      case 'clipboard-write':
      case 'clipboard-read':
        if (appPermissions && !appPermissions.includes('clipboard')) {
          reply(_replyId, null, 'Permission denied: clipboard')
          break
        }
        if (action === 'clipboard-write') {
          navigator.clipboard.writeText(e.data.text || '')
            .then(() => reply(_replyId, true))
            .catch(err => reply(_replyId, null, err.message))
        } else {
          navigator.clipboard.readText()
            .then(text => reply(_replyId, text))
            .catch(err => reply(_replyId, null, err.message))
        }
        break

      // ── Inter-app events ──
      case 'event-emit': {
        // SEC-002: Force namespace to this frame's actual slug (parent-controlled)
        const rawEvent = String(e.data.event || '')
        const colonIdx = rawEvent.indexOf(':')
        const eventName = colonIdx >= 0 ? rawEvent.slice(colonIdx + 1) : rawEvent
        const namespacedEvent = slug + ':' + eventName
        // Broadcast to all other app iframes
        document.querySelectorAll<HTMLIFrameElement>('iframe.page-frame').forEach(f => {
          if (f !== iframe && f.contentWindow) {
            f.contentWindow.postMessage(
              { type: 'alf', action: 'event-relay', event: namespacedEvent, payload: e.data.payload },
              location.origin
            )
          }
        })
        break
      }

      // ── Badge (SEC-P03: force to own slug, prevent cross-app spoofing) ──
      case 'badge-set':
        nav.setBadge('page:' + slug, e.data.count || 0)
        break
      case 'badge-increment':
        nav.incrementBadge('page:' + slug)
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
    // Collect form input values from the sheet (inputs, selects, textareas with name or data-field)
    const sheetContent = target.closest('.sdk-sheet-content')
    if (sheetContent) {
      sheetContent.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(
        'input[name], select[name], textarea[name], input[data-field], select[data-field], textarea[data-field]'
      ).forEach(el => {
        const field = el.getAttribute('data-field') || el.name
        if (!field) return
        if (el instanceof HTMLInputElement && el.type === 'checkbox') {
          params[field] = el.checked ? 'true' : 'false'
        } else {
          params[field] = el.value
        }
      })
    }
    // Relay to iframe
    iframe?.contentWindow?.postMessage(
      { type: 'alf', action: 'sheet-action', name: actionName, params },
      location.origin
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
        injectUICSS(iframe)
        injectSheetCSS(iframe)
        injectSafeAreas(iframe)
        // Reveal iframe after CSS has loaded (next frame ensures paint)
        requestAnimationFrame(() => { iframeReady = true })
        // Send permissions to iframe after load
        fetch('/api/apps/' + slug + '/permissions')
          .then(r => r.ok ? r.json() : null)
          .then(data => {
            if (data) {
              appPermissions = data.permissions
              if (iframe?.contentWindow) {
                iframe.contentWindow.postMessage(
                  { type: 'alf', action: 'permissions', permissions: data.permissions },
                  location.origin
                )
              }
            }
          })
          .catch(() => {}) // fail silently — app gets all permissions
      })
    }

    // Forward browser visibility changes to iframe
    document.addEventListener('visibilitychange', onVisibilityChange)

    return () => window.removeEventListener('message', handleMessage)
  })

  onDestroy(() => {
    document.removeEventListener('visibilitychange', onVisibilityChange)
    postToIframe('hidden')
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
  class:ready={iframeReady}
  src={`/apps/${slug}/${query}`}
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
    <p class="text-dim">{confirmMsg}</p>
    <div class="btn-group" style="justify-content:flex-end">
      <button class="btn" onclick={() => handleConfirm(false)}>{confirmCancel}</button>
      <button class="btn btn-primary" onclick={() => handleConfirm(true)}>{confirmOk}</button>
    </div>
  </div>
</Modal>

<!-- Prompt dialog -->
<Modal open={promptOpen} onclose={handlePromptCancel}>
  <div class="sdk-dialog">
    {#if promptTitle}<h3>{promptTitle}</h3>{/if}
    <p class="text-dim">{promptMsg}</p>
    {#if promptMultiline}
      <textarea
        class="textarea"
        bind:value={promptValue}
        placeholder={promptPlaceholder}
        rows={4}
      ></textarea>
    {:else}
      <input
        type="text"
        class="input"
        bind:value={promptValue}
        placeholder={promptPlaceholder}
        onkeydown={(e: KeyboardEvent) => { if (e.key === 'Enter') handlePromptSubmit() }}
      />
    {/if}
    <div class="btn-group" style="justify-content:flex-end">
      <button class="btn" onclick={handlePromptCancel}>Cancel</button>
      <button class="btn btn-primary" onclick={handlePromptSubmit}>{promptOk}</button>
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
    opacity: 0;
    transition: opacity 0.1s ease;
  }
  .page-frame.ready {
    opacity: 1;
  }

  @media (max-width: 768px) {
    .page-frame {
      top: calc(36px + env(safe-area-inset-top, 0px));
      height: calc(100% - 36px - env(safe-area-inset-top, 0px));
    }
  }

  /* Base styles for sheet HTML content — apps provide raw HTML without
     their own stylesheet, so we need sensible defaults. */
  .sdk-sheet-content {
    font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
    font-size: var(--font-sm, 13px);
    line-height: 1.5;
    color: var(--text);
  }

  .sdk-sheet-content :global(h1),
  .sdk-sheet-content :global(h2),
  .sdk-sheet-content :global(h3),
  .sdk-sheet-content :global(h4) {
    margin: 0 0 0.5rem;
    font-weight: 600;
  }
  .sdk-sheet-content :global(h3) { font-size: var(--font-md, 15px); }

  .sdk-sheet-content :global(p) {
    margin: 0 0 0.5rem;
    color: var(--text-dim);
  }

  .sdk-sheet-content :global(img) {
    max-width: 100%;
    height: auto;
    border-radius: var(--radius, 8px);
    margin-bottom: 0.75rem;
  }

  .sdk-sheet-content :global(button) {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 6px 14px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    cursor: pointer;
    transition: background 0.15s;
  }
  .sdk-sheet-content :global(button:hover) {
    border-color: var(--accent);
    background: var(--border);
  }
  .sdk-sheet-content :global(button.active),
  .sdk-sheet-content :global(button[aria-pressed="true"]) {
    background: var(--accent);
    color: var(--on-accent);
    border-color: var(--accent);
  }

  .sdk-sheet-content :global(input),
  .sdk-sheet-content :global(select),
  .sdk-sheet-content :global(textarea) {
    width: 100%;
    padding: 8px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius, 8px);
    background: var(--bg-input);
    color: var(--text);
    font-family: inherit;
    font-size: var(--font-sm, 13px);
    margin-bottom: 0.5rem;
  }
  .sdk-sheet-content :global(input:focus),
  .sdk-sheet-content :global(select:focus),
  .sdk-sheet-content :global(textarea:focus) {
    outline: 2px solid var(--accent);
    outline-offset: -1px;
  }

  .sdk-sheet-content :global(label) {
    display: block;
    font-size: var(--font-sm, 13px);
    color: var(--text-dim);
    margin-bottom: 2px;
    font-weight: 500;
  }

  .sdk-sheet-content :global(hr) {
    border: none;
    border-top: 1px solid var(--border);
    margin: 0.75rem 0;
  }

  .sdk-dialog {
    display: flex;
    flex-direction: column;
    gap: var(--space-sm, 8px);
  }

  .sdk-dialog h3 {
    margin: 0;
    font-size: var(--font-lg, 18px);
    font-weight: 600;
  }

  .sdk-dialog p {
    margin: 0;
    line-height: 1.5;
  }
</style>
