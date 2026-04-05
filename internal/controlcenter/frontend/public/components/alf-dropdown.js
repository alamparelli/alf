import { fire } from './_helpers.js';
class AlfDropdown extends HTMLElement {
  connectedCallback() {
    const trigger = this.querySelector('[slot="trigger"]');
    const items = Array.from(this.querySelectorAll('alf-menu-item, alf-menu-divider'));
    const wrap = document.createElement('div');
    wrap.className = 'dropdown';
    const menu = document.createElement('div');
    menu.className = 'dropdown-menu';
    items.forEach(item => {
      if (item.tagName === 'ALF-MENU-DIVIDER') {
        menu.innerHTML += '<hr class="divider">';
      } else {
        const btn = document.createElement('button');
        btn.className = 'dropdown-item' + (item.hasAttribute('danger') ? ' danger' : '');
        btn.textContent = item.textContent;
        btn.addEventListener('click', () => {
          wrap.classList.remove('open');
          fire(this, 'alf-select', { value: item.getAttribute('value') || '' });
        });
        menu.appendChild(btn);
      }
      item.style.display = 'none';
    });
    if (trigger) {
      trigger.removeAttribute('slot');
      trigger.addEventListener('click', (e) => {
        e.stopPropagation();
        wrap.classList.toggle('open');
      });
      wrap.appendChild(trigger);
    }
    wrap.appendChild(menu);
    this.appendChild(wrap);
    document.addEventListener('click', () => wrap.classList.remove('open'));
  }
}
class AlfMenuItem extends HTMLElement {}
class AlfMenuDivider extends HTMLElement {}
customElements.define('alf-dropdown', AlfDropdown);
customElements.define('alf-menu-item', AlfMenuItem);
customElements.define('alf-menu-divider', AlfMenuDivider);
