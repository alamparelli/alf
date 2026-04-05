# Content Card

Card with media area + body. Use for galleries, feature grids.

```html
<div class="content-card">
  <div class="content-card-media">
    <!-- image or icon placeholder -->
  </div>
  <div class="content-card-body">
    <div class="text-bold text-sm mb-xs">Card Title</div>
    <p class="text-xs text-dim m-0">Description text.</p>
  </div>
</div>
```

Tinted media area (dynamic color — inline style acceptable here):
```html
<div class="content-card">
  <div class="content-card-media" style="background:color-mix(in srgb, var(--accent) 12%, var(--bg))">
    <!-- icon -->
  </div>
  <div class="content-card-body">
    <div class="text-bold text-sm mb-xs">Title</div>
    <p class="text-xs text-dim m-0">Description.</p>
  </div>
</div>
```
