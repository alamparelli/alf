import { fire, ICONS } from './_helpers.js';
class AlfChip extends HTMLElement {
  connectedCallback() {
    const text = this.textContent;
    this.classList.add('chip');
    this.innerHTML = `<span>${text}</span><button class="chip-remove">${ICONS.close}</button>`;
    this.querySelector('.chip-remove').addEventListener('click', () => {
      fire(this, 'alf-remove', { value: text });
      this.remove();
    });
  }
}
customElements.define('alf-chip', AlfChip);
