class AlfCarousel extends HTMLElement {
  connectedCallback() {
    const variant = this.getAttribute('variant') || 'cards';
    this.classList.add('carousel', `carousel-${variant}`);
    const items = Array.from(this.children);
    const track = document.createElement('div');
    track.className = 'carousel-track';
    items.forEach(item => {
      const wrap = document.createElement('div');
      wrap.className = 'carousel-item';
      wrap.appendChild(item);
      track.appendChild(wrap);
    });
    this.innerHTML = '';
    this.appendChild(track);
  }
}
customElements.define('alf-carousel', AlfCarousel);
