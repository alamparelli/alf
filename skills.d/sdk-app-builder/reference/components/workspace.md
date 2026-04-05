# Workspace Layouts

Sidebar + main content panels. Use for apps with navigation (mail, notes, IDE).

> On mobile (≤768px), sidebar is hidden by default. Add `.sidebar-open` to `.workspace` to show it. Use `.workspace-back` button in main header for navigation back.

## 2-panel (sidebar + main)

```html
<div class="workspace">
  <div class="workspace-sidebar">
    <div class="workspace-sidebar-header">
      <!-- icon --> Workspace
    </div>
    <div class="workspace-sidebar-body">
      <div class="sidebar-section">
        <div class="sidebar-section-title">Navigation</div>
        <nav class="sidebar-nav">
          <button class="sidebar-item active" onclick="selectView('dashboard')">
            <!-- icon --> Dashboard
          </button>
          <button class="sidebar-item" onclick="selectView('docs')">
            <!-- icon --> Documents
            <span class="count-badge">3</span>
          </button>
          <button class="sidebar-item" onclick="selectView('contacts')">
            <!-- icon --> Contacts
          </button>
        </nav>
      </div>
    </div>
    <div class="workspace-sidebar-footer">
      <button class="sidebar-item"><!-- icon --> Settings</button>
    </div>
  </div>
  <div class="workspace-main">
    <div class="workspace-main-header">
      <button class="workspace-back" onclick="toggleSidebar()">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M15 18l-6-6 6-6"/></svg>
      </button>
      <h3 class="text-sm text-bold m-0">Dashboard</h3>
      <span class="spacer"></span>
      <div class="search-box">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/></svg>
        <input type="text" placeholder="Search...">
      </div>
    </div>
    <div class="workspace-main-body">
      <!-- main content -->
    </div>
  </div>
</div>
```

## 3-panel (sidebar + list + detail)

For mail, chat, or any list→detail pattern. The middle list panel reuses `workspace-main` with a fixed width.

```html
<div class="workspace">
  <!-- Narrow sidebar -->
  <div class="workspace-sidebar">
    <div class="workspace-sidebar-header"><!-- icon --> Mail</div>
    <div class="workspace-sidebar-body">
      <nav class="sidebar-nav">
        <button class="sidebar-item active">Inbox <span class="count-badge">5</span></button>
        <button class="sidebar-item">Sent</button>
      </nav>
    </div>
  </div>
  <!-- List panel -->
  <div class="workspace-detail">
    <div class="workspace-detail-header">
      <span class="text-sm text-bold">Inbox</span>
      <span class="spacer"></span>
      <span class="text-xs text-dim">5 new</span>
    </div>
    <div class="workspace-detail-body">
      <div class="list-item-interactive flex-col items-start gap-xs">
        <div class="flex items-center gap-xs w-full">
          <span class="text-sm text-bold flex-1">Alice</span>
          <span class="text-xs text-dim">2m</span>
        </div>
        <span class="text-xs truncate">Subject line here</span>
      </div>
    </div>
  </div>
  <!-- Main content panel -->
  <div class="workspace-main">
    <div class="workspace-main-header">
      <button class="workspace-back" onclick="toggleSidebar()">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M15 18l-6-6 6-6"/></svg>
      </button>
      <span class="text-sm text-bold">Subject</span>
    </div>
    <div class="workspace-main-body">
      <!-- detail content -->
    </div>
  </div>
</div>
```

## Mobile toggle pattern

```js
function toggleSidebar() {
  document.querySelector('.workspace').classList.toggle('sidebar-open');
}

// Call from sidebar items to close sidebar on mobile after selection
function selectView(view) {
  document.querySelector('.workspace').classList.remove('sidebar-open');
  // ... update main content
}
```

## Classes reference

| Class | What |
|-------|------|
| `.workspace` | Root flex container, full viewport height |
| `.workspace--embed` | Variant: `height: 100%` instead of `100vh` (for iframes) |
| `.workspace-sidebar` | Left panel (240px, `--bg-card`, border-right) |
| `.workspace-sidebar-header` | Sidebar top area (title, icon) |
| `.workspace-sidebar-body` | Scrollable sidebar content |
| `.workspace-sidebar-footer` | Sidebar bottom area (settings, account) |
| `.sidebar-section` | Group inside sidebar body |
| `.sidebar-section-title` | Uppercase section label |
| `.sidebar-nav` | Vertical nav list (gap: 1px) |
| `.sidebar-item` | Nav button (hover, `.active` state) |
| `.workspace-back` | Back button, hidden on desktop, shown on mobile |
| `.workspace-main` | Right panel (flex: 1, min-width: 0) |
| `.workspace-main-header` | Main top bar (48px min-height, border-bottom) |
| `.workspace-main-body` | Scrollable main content (padding: `--space-md`) |
| `.workspace-detail` | Third panel (320px, border-left) — hidden on mobile |
| `.workspace-detail-header` | Detail top bar |
| `.workspace-detail-body` | Scrollable detail content |
| `.sidebar-open` | Modifier on `.workspace` — shows sidebar fullscreen on mobile |

## Common mistakes

| Wrong | Correct | Why |
|-------|---------|-----|
| Putting list items directly in `.workspace-sidebar` | Put them in `.workspace-sidebar-body` | Only `-body` scrolls; header/footer stay fixed |
| Adding `overflow-y: auto` to `.workspace-sidebar` | Already on `.workspace-sidebar-body` | The sidebar itself uses `overflow: hidden` so header/footer don't scroll away |
| Making main content not fill the panel | Use `.flex-1` on content inside `.workspace-main-body` | The body is `flex: 1` with `overflow-y: auto`, children need `flex: 1` to fill it |
| Using `el.style.display = 'none'` to toggle panels | Use `el.classList.add('hidden')` | `.hidden` uses `!important` to safely override `.flex-col`, `.flex`, etc. |

## Utility classes used in examples

All exist in `alf-ui.css` — see loading.md for full utility list:
`.flex-1`, `.flex-col`, `.items-start`, `.items-center`, `.gap-xs`, `.gap-sm`, `.w-full`, `.m-0`, `.text-sm`, `.text-xs`, `.text-bold`, `.text-dim`, `.truncate`, `.spacer`, `.count-badge`, `.search-box`
