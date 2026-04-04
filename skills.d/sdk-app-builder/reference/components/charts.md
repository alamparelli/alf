# Charts

## Bar chart (vertical)

```html
<div class="bar-chart" style="height:120px">
  <div class="bar-chart-col">
    <div class="bar-chart-bar" style="height:40%"></div>
    <span class="bar-chart-label">Mon</span>
  </div>
  <div class="bar-chart-col">
    <div class="bar-chart-bar" style="height:80%"></div>
    <span class="bar-chart-label">Tue</span>
  </div>
  <div class="bar-chart-col">
    <div class="bar-chart-bar" style="height:60%; background:var(--green)"></div>
    <span class="bar-chart-label">Wed</span>
  </div>
</div>
```

## Horizontal bar chart

```html
<div class="hbar-row">
  <span class="hbar-label">Design</span>
  <div class="hbar-track"><div class="hbar-fill" style="width:85%"></div></div>
  <span class="hbar-value">85%</span>
</div>
<div class="hbar-row">
  <span class="hbar-label">Dev</span>
  <div class="hbar-track"><div class="hbar-fill" style="width:62%; background:var(--sapphire)"></div></div>
  <span class="hbar-value">62%</span>
</div>
```

## Ring chart

SVG donut. `stroke-dashoffset = 213.6 * (1 - percentage/100)`.

```html
<div class="ring-chart">
  <svg viewBox="0 0 80 80">
    <circle class="ring-bg" cx="40" cy="40" r="34"/>
    <circle class="ring-fill" cx="40" cy="40" r="34"
      stroke-dasharray="213.6" stroke-dashoffset="55.5"/>
  </svg>
  <span class="ring-chart-value">74%</span>
</div>
```

Override color: `style="stroke:var(--green)"` on `.ring-fill`.
