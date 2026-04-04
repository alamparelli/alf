# Nav Row

Inline prev/next navigation with centered label. Use for week/month navigation.

```html
<div class="nav-row">
  <button class="btn btn-sm" onclick="prev()">
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 18l-6-6 6-6"/></svg>
  </button>
  <span class="nav-label">Mar 30 - Apr 5</span>
  <button class="btn btn-sm" onclick="next()">
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 18l6-6-6-6"/></svg>
  </button>
  <button class="btn btn-sm" onclick="goToday()">Today</button>
</div>
```
