# Day Grid

Calendar day cells for habit trackers, heatmaps, attendance. 28px squares with states.

```html
<div class="day-grid-labels">
  <span>Mon</span><span>Tue</span><span>Wed</span><span>Thu</span><span>Fri</span><span>Sat</span><span>Sun</span>
</div>
<div class="day-grid">
  <button class="day-cell">28</button>
  <button class="day-cell">29</button>
  <button class="day-cell active">30</button>
  <button class="day-cell today">1</button>
  <button class="day-cell future">2</button>
</div>
```

States:
- `.active` — completed/selected (accent bg)
- `.today` — current day (accent outline)
- `.future` — not yet reachable (dimmed, no cursor)

Use inside a `.list-item-interactive` or `.card` for per-row calendars (habit tracker pattern):
```html
<div class="card-group">
  <div class="list-item-interactive">
    <span style="flex:1">Exercise</span>
    <div class="day-grid">
      <button class="day-cell active">1</button>
      <button class="day-cell">2</button>
      <button class="day-cell today">3</button>
    </div>
  </div>
</div>
```
