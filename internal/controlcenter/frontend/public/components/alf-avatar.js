import { esc } from './_helpers.js';
class AlfAvatar extends HTMLElement {
  connectedCallback() {
    const initials = this.getAttribute('initials') || '?';
    const size = this.getAttribute('size') || '';
    const color = this.getAttribute('color') || 'accent';
    this.className = 'avatar' + (size ? ` avatar-${size}` : '');
    this.style.background = `var(--${color})`;
    this.textContent = initials;
  }
}
customElements.define('alf-avatar', AlfAvatar);
