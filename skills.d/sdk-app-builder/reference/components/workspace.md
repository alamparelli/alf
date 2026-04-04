# Workspace Layouts

Sidebar + main content panels. Use for apps with navigation.

## 2-panel (sidebar + main)

```html
<div style="display:flex; height:100%; overflow:hidden;">
  <div class="workspace-sidebar">
    <div class="workspace-sidebar-header">
      <!-- icon --> Workspace
    </div>
    <div class="workspace-sidebar-body">
      <div class="sidebar-section">
        <div class="sidebar-section-title">Navigation</div>
        <nav class="sidebar-nav">
          <button class="sidebar-item active"><!-- icon --> Dashboard</button>
          <button class="sidebar-item"><!-- icon --> Documents
            <span class="count-badge">3</span>
          </button>
          <button class="sidebar-item"><!-- icon --> Contacts</button>
        </nav>
      </div>
    </div>
    <div class="workspace-sidebar-footer">
      <button class="sidebar-item"><!-- icon --> Settings</button>
    </div>
  </div>
  <div style="flex:1; display:flex; flex-direction:column; min-width:0;">
    <div class="workspace-main-header">
      <h3 style="margin:0; font-size:var(--font-sm);">Dashboard</h3>
      <span class="spacer"></span>
      <div class="search-box">
        <!-- search icon -->
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

```html
<div style="display:flex; height:100%; overflow:hidden;">
  <!-- Narrow sidebar -->
  <div style="width:180px; flex-shrink:0; display:flex; flex-direction:column; background:var(--bg-card); border-right:1px solid color-mix(in srgb, var(--border) 40%, transparent);">
    <div class="workspace-sidebar-header"><!-- icon --> Mail</div>
    <div style="flex:1; overflow-y:auto; padding:0 4px 4px;">
      <nav class="sidebar-nav">
        <button class="sidebar-item active">Inbox <span class="count-badge">5</span></button>
        <button class="sidebar-item">Sent</button>
      </nav>
    </div>
  </div>
  <!-- List panel -->
  <div style="width:220px; flex-shrink:0; display:flex; flex-direction:column; border-right:1px solid color-mix(in srgb, var(--border) 40%, transparent);">
    <div class="workspace-main-header">
      <span class="text-sm text-bold">Inbox</span>
      <span class="spacer"></span>
      <span class="text-xs text-dim">5 new</span>
    </div>
    <div style="flex:1; overflow-y:auto;">
      <div class="list-item-interactive" style="padding:10px 12px; flex-direction:column; align-items:stretch; gap:2px;">
        <div class="flex items-center gap-xs">
          <span class="text-sm text-bold" style="flex:1;">Alice</span>
          <span class="text-xs text-dim">2m</span>
        </div>
        <span class="text-xs truncate">Subject line here</span>
      </div>
    </div>
  </div>
  <!-- Detail panel -->
  <div style="flex:1; display:flex; flex-direction:column; min-width:0;">
    <div class="workspace-detail-header">
      <span class="text-sm text-bold">Subject</span>
    </div>
    <div style="flex:1; overflow-y:auto; padding:16px;">
      <!-- detail content -->
    </div>
  </div>
</div>
```
