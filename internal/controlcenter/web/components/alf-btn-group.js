import { fire } from './_helpers.js';
class AlfBtnGroup extends HTMLElement {
  connectedCallback() {
    const val = this.getAttribute('value') || '';
    this.classList.add('btn-group');
    this._update(val);
    this.addEventListener('click', (e) => {
      const btn = e.target.closest('[data-value]');
      if (!btn) return;
      this.setAttribute('value', btn.dataset.value);
      this._update(btn.dataset.value);
      fire(this, 'alf-change', { value: btn.dataset.value });
    });
  }
  _update(val) {
    this.querySelectorAll('[data-value]').forEach(b => {
      b.classList.toggle('active', b.dataset.value === val);
    });
  }
}
customElements.define('alf-btn-group', AlfBtnGroup);
