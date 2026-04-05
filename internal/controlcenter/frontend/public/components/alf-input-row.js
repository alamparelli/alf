import { fire, esc } from './_helpers.js';
class AlfInputRow extends HTMLElement {
  connectedCallback() {
    const ph = this.getAttribute('placeholder') || '';
    const btn = this.getAttribute('button') || 'Add';
    const name = this.getAttribute('name') || '';
    this.classList.add('input-row');
    this.innerHTML = `<input class="input flex-1" type="text" name="${esc(name)}" placeholder="${esc(ph)}"><button class="btn btn-primary btn-sm">${esc(btn)}</button>`;
    const input = this.querySelector('input');
    const submit = () => {
      if (!input.value.trim()) return;
      fire(this, 'alf-submit', { value: input.value.trim() });
      input.value = '';
    };
    this.querySelector('button').addEventListener('click', submit);
    input.addEventListener('keydown', e => { if (e.key === 'Enter') submit(); });
  }
}
customElements.define('alf-input-row', AlfInputRow);
