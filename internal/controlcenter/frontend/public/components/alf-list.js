import { fire } from './_helpers.js';
class AlfList extends HTMLElement {
  connectedCallback() {
    if (!this.querySelector('.card-group')) {
      const wrap = document.createElement('div');
      wrap.className = 'card-group';
      while (this.firstChild) wrap.appendChild(this.firstChild);
      this.appendChild(wrap);
    }
  }
}
class AlfListItem extends HTMLElement {
  connectedCallback() {
    const hasCheckbox = this.hasAttribute('checkbox');
    const isChecked = this.hasAttribute('checked');
    if (hasCheckbox) {
      const check = document.createElement('div');
      check.className = 'check' + (isChecked ? ' checked' : '');
      check.addEventListener('click', () => {
        this.checked = !this.checked;
      });
      this.insertBefore(check, this.firstChild);
    }
    this.classList.add('list-item-interactive');
  }
  get checked() { const c = this.querySelector('.check'); return c ? c.classList.contains('checked') : false; }
  set checked(v) {
    const c = this.querySelector('.check');
    if (c) { c.classList.toggle('checked', v); this.toggleAttribute('checked', v); fire(this, 'alf-check-change', { checked: v }); }
  }
}
customElements.define('alf-list', AlfList);
customElements.define('alf-list-item', AlfListItem);
