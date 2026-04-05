class AlfAccordion extends HTMLElement {
  connectedCallback() { this.classList.add('accordion'); }
}
class AlfAccordionItem extends HTMLElement {
  connectedCallback() {
    const label = this.getAttribute('label') || '';
    const isOpen = this.hasAttribute('open');
    const content = this.innerHTML;
    this.classList.add('accordion-item');
    if (isOpen) this.classList.add('open');
    this.innerHTML = `<button class="accordion-header">${label}<svg class="accordion-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M6 9l6 6 6-6"/></svg></button><div class="accordion-body">${content}</div>`;
    this.querySelector('.accordion-header').addEventListener('click', () => {
      this.classList.toggle('open');
    });
  }
}
customElements.define('alf-accordion', AlfAccordion);
customElements.define('alf-accordion-item', AlfAccordionItem);
