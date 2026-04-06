class AlfDangerZone extends HTMLElement {
  connectedCallback() { this.classList.add('danger-zone'); }
}
customElements.define('alf-danger-zone', AlfDangerZone);
