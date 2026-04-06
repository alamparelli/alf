class AlfTag extends HTMLElement {
  connectedCallback() {
    const variant = this.getAttribute('variant') || 'accent';
    const outline = this.hasAttribute('outline') ? ' tag-outline' : '';
    this.className = `tag tag-${variant}${outline}`;
  }
}
customElements.define('alf-tag', AlfTag);
