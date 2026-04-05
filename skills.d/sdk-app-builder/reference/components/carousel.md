# Carousel

Scrollable content with dot indicators.

## Card carousel

```html
<div class="carousel carousel-cards">
  <div class="carousel-item">
    <div class="card" style="height:140px; width:260px;">
      <!-- card content -->
    </div>
  </div>
  <div class="carousel-item">
    <div class="card" style="height:140px; width:260px;">
      <!-- card content -->
    </div>
  </div>
</div>
<div class="carousel-dots">
  <button class="carousel-dot active"></button>
  <button class="carousel-dot"></button>
</div>
```

## Peek carousel (full-width with peek)

```html
<div class="carousel carousel-peek">
  <div class="carousel-item">
    <div class="card">Slide 1 content</div>
  </div>
  <div class="carousel-item">
    <div class="card">Slide 2 content</div>
  </div>
</div>
```

## Full-width carousel

```html
<div class="carousel carousel-full overflow-hidden">
  <div class="carousel-item">
    <div class="aspect-video">
      <!-- slide content (set background via JS for dynamic tints) -->
    </div>
  </div>
</div>
```
