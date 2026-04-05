# Loading & Utilities

**Keyboard shortcut:**
```html
Press <span class="kbd">⌘K</span> to search
```

**Skeleton loading:**
```html
<div class="skeleton skeleton-text"></div>
<div class="skeleton skeleton-text"></div>
<div class="skeleton skeleton-text"></div>
```

**Meta (icon + text):**
```html
<span class="meta"><!-- 12px icon --> v1.2.0</span>
```

**Done state (strikethrough):**
```html
<span class="done">Completed task</span>
```

**Spacer (flex:1 push):**
```html
<div class="flex items-center">
  <h3>Title</h3>
  <div class="spacer"></div>
  <button class="btn btn-sm">Action</button>
</div>
```

**Divider:**
```html
<hr class="divider">
```

**Empty state:**
```html
<div class="empty-state">
  <!-- 48px icon -->
  <p>No items yet.</p>
  <button class="btn btn-primary">Create</button>
</div>
```

**Footer stats:**
```html
<div class="footer-stats">3 / 10 items</div>
```

**Section label:**
```html
<div class="section-label">In progress</div>
```

**Danger zone:**
```html
<div class="danger-zone">
  <h3>Delete account</h3>
  <p>This action is irreversible.</p>
  <button class="btn btn-danger">Delete</button>
</div>
```

**Status dots:**
```html
<span class="dot dot-success"></span> Online
<span class="dot dot-danger"></span> Offline
<span class="dot dot-warning"></span> Pending
```

**Count badge:**
```html
<span class="count-badge">5</span>
```

Accent-filled pill. Use next to labels, tabs, nav items.

**Carousel dots:**
```html
<div class="carousel-dots">
  <button class="carousel-dot active"></button>
  <button class="carousel-dot"></button>
  <button class="carousel-dot"></button>
</div>
```

Active dot stretches to pill shape.

**Change indicators:**
```html
<span class="change-up">+5.2%</span>
<span class="change-down">-1.8%</span>
```

Arrow prefix auto-inserted via CSS `::before`.
