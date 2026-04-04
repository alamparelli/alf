# Buttons

Bordered with subtle shadow. Variants override background + border-color.

```html
<button class="btn">Default</button>
<button class="btn btn-primary">Primary</button>
<button class="btn btn-danger">Delete</button>
<button class="btn btn-success">Confirm</button>
<button class="btn btn-ghost">Ghost</button>
<button class="btn btn-outline">Outline</button>
<button class="btn btn-outline-danger">Outline Danger</button>
<button class="btn btn-sm">Small</button>
<button class="btn btn-lg">Large</button>
<button class="btn btn-block">Full Width</button>
<button class="btn" disabled>Disabled</button>
```

Icon button (no border, dim color, hover reveals bg):
```html
<button class="btn-icon"><!-- 16px SVG icon --></button>
```

Button with icon:
```html
<button class="btn btn-sm">
  <!-- 14px Lucide SVG --> Refresh
</button>
```

Button group:
```html
<div class="btn-group">
  <button class="btn">Left</button>
  <button class="btn">Center</button>
  <button class="btn">Right</button>
</div>
```

Toggle button (active state):
```html
<div class="btn-group">
  <button class="btn btn-sm active">1x</button>
  <button class="btn btn-sm">2x</button>
  <button class="btn btn-sm">5x</button>
</div>
```

`.btn.active` fills with `--accent`. Toggle via JS.
