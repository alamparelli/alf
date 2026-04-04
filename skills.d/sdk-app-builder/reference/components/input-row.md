# Input Row & Form Row

## Input row (input + button combo)

```html
<div class="input-row">
  <input class="input" placeholder="Add a new item...">
  <button class="btn btn-primary">Add</button>
</div>
```

With icon button:
```html
<div class="input-row">
  <input class="input" placeholder="Quick add...">
  <button class="btn-icon-accent">
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 5v14M5 12h14"/></svg>
  </button>
</div>
```

## Form row (side-by-side fields)

```html
<div class="form-row">
  <div class="form-group">
    <label class="form-label">First name</label>
    <input class="input" placeholder="John">
  </div>
  <div class="form-group">
    <label class="form-label">Last name</label>
    <input class="input" placeholder="Doe">
  </div>
</div>
```
