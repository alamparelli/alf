import { fire } from './_helpers.js';
class AlfPagination extends HTMLElement {
  static get observedAttributes() { return ['total', 'page-size', 'current']; }
  connectedCallback() { this._render(); }
  attributeChangedCallback() { this._render(); }
  _render() {
    const total = parseInt(this.getAttribute('total') || '0');
    const size = parseInt(this.getAttribute('page-size') || '10');
    const current = parseInt(this.getAttribute('current') || '1');
    const pages = Math.ceil(total / size);
    if (pages <= 1) { this.innerHTML = ''; return; }
    let html = '<div class="pagination">';
    for (let i = 1; i <= pages; i++) {
      html += `<button class="pagination-btn${i === current ? ' active' : ''}" data-page="${i}">${i}</button>`;
    }
    html += '</div>';
    this.innerHTML = html;
    this.querySelectorAll('button').forEach(btn => {
      btn.addEventListener('click', () => {
        this.setAttribute('current', btn.dataset.page);
        fire(this, 'alf-page-change', { page: parseInt(btn.dataset.page) });
      });
    });
  }
}
customElements.define('alf-pagination', AlfPagination);
