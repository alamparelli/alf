# Stat Grid

4-column fused grid with 1px gap borders. Each item has a colored accent bar.

> `.stat-bar` background is set via inline style — this is the accepted pattern for per-item category colors.

```html
<div class="stat-grid">
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--sapphire)"></div>
    <div class="stat-value">15,040</div>
    <div class="stat-label">Revenue</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--green)"></div>
    <div class="stat-value">1,480</div>
    <div class="stat-label">Users</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--yellow)"></div>
    <div class="stat-value">92%</div>
    <div class="stat-label">Uptime</div>
  </div>
  <div class="stat-item">
    <div class="stat-bar" style="background:var(--red)"></div>
    <div class="stat-value">3</div>
    <div class="stat-label">Errors</div>
  </div>
</div>
```
