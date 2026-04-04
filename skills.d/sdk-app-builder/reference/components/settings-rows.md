# Settings Rows

Label + description on the left, control on the right. Auto-separators between rows.

```html
<div class="card">
  <div class="settings-row">
    <div>
      <div class="settings-row-label">Notifications</div>
      <div class="settings-row-desc">Receive alerts when tasks complete</div>
    </div>
    <label class="toggle"><input type="checkbox" checked><span class="toggle-track"></span></label>
  </div>
  <div class="settings-row">
    <div>
      <div class="settings-row-label">Language</div>
      <div class="settings-row-desc">Display language</div>
    </div>
    <select class="select" style="width:140px"><option>English</option></select>
  </div>
</div>
```
