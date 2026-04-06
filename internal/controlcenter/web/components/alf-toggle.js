import { fire } from './_helpers.js';
class AlfToggle extends HTMLElement {
  connectedCallback() {
    const checked = this.hasAttribute('checked');
    const name = this.getAttribute('name') || '';
    this.innerHTML = `<label class="toggle"><input type="checkbox" ${checked ? 'checked' : ''} name="${name}"><span class="toggle-slider"></span></label>`;
    this.querySelector('input').addEventListener('change', (e) => {
      this.toggleAttribute('checked', e.target.checked);
      fire(this, 'alf-change', { checked: e.target.checked, name });
    });
  }
  get checked() { const el = this.querySelector('input'); return el ? el.checked : false; }
  set checked(v) { const el = this.querySelector('input'); if (el) { el.checked = v; this.toggleAttribute('checked', v); } }
}
customElements.define('alf-toggle', AlfToggle);
