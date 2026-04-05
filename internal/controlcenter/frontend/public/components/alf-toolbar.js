import { fire, ICONS } from './_helpers.js';
class AlfToolbar extends HTMLElement {
  connectedCallback() {
    const searchPh = this.getAttribute('search') || '';
    this.classList.add('toolbar');
    if (searchPh) {
      const box = document.createElement('div');
      box.className = 'search-box';
      box.style.flex = '1';
      box.innerHTML = `${ICONS.search}<input type="text" placeholder="${searchPh}">`;
      const input = box.querySelector('input');
      input.addEventListener('input', () => fire(this, 'alf-search', { value: input.value }));
      this.insertBefore(box, this.firstChild);
      this._searchInput = input;
    }
  }
  get searchValue() { return this._searchInput ? this._searchInput.value : ''; }
}
customElements.define('alf-toolbar', AlfToolbar);
