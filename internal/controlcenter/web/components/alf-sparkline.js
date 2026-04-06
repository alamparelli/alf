class AlfSparkline extends HTMLElement {
  connectedCallback() { this.classList.add('sparkline'); }
  set data(v) {
    this._data = v || [];
    const color = this.getAttribute('color') || 'accent';
    const max = Math.max(...this._data, 1);
    const w = 100, h = 30;
    const points = this._data.map((d, i) => `${(i / (this._data.length - 1)) * w},${h - (d / max) * h}`).join(' ');
    this.innerHTML = `<svg viewBox="0 0 ${w} ${h}" preserveAspectRatio="none" style="width:100%;height:${h}px"><polyline points="${points}" fill="none" stroke="var(--${color})" stroke-width="2"/></svg>`;
  }
  get data() { return this._data || []; }
}
customElements.define('alf-sparkline', AlfSparkline);
