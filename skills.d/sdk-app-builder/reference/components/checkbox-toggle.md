# Checkbox & Toggle

## Checkbox

Square 16px checkbox with rounded corners. Toggle `.checked` via JS.

```html
<!-- Unchecked -->
<div class="check" onclick="this.classList.toggle('checked')"></div>

<!-- Checked -->
<div class="check checked"></div>

<!-- In a list item -->
<div class="list-item-interactive">
  <div class="check"></div>
  <span style="flex:1">Task text</span>
</div>
```

## Toggle switch

```html
<label class="toggle">
  <input type="checkbox">
  <span class="toggle-track"></span>
  Enable feature
</label>

<!-- Checked -->
<label class="toggle">
  <input type="checkbox" checked>
  <span class="toggle-track"></span>
  Active
</label>
```
