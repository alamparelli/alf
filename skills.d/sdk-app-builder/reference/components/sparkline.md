# Sparkline

Inline mini bar chart for trend visualization.

```html
<span class="sparkline">
  <span class="sparkline-bar" style="height:40%"></span>
  <span class="sparkline-bar" style="height:65%"></span>
  <span class="sparkline-bar" style="height:45%"></span>
  <span class="sparkline-bar" style="height:80%"></span>
  <span class="sparkline-bar" style="height:60%"></span>
  <span class="sparkline-bar" style="height:90%"></span>
  <span class="sparkline-bar" style="height:75%"></span>
</span>
```

Override color for different metrics:
```html
<span class="sparkline">
  <span class="sparkline-bar" style="height:20%; background:var(--red)"></span>
  <span class="sparkline-bar" style="height:50%; background:var(--red)"></span>
</span>
```

Pair with change indicators:
```html
<div class="flex items-center gap-sm">
  <span class="text-sm">Revenue</span>
  <span class="sparkline"><!-- bars --></span>
  <span class="change-up text-xs">+12%</span>
</div>
```
