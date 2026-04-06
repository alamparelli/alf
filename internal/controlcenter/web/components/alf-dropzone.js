import { fire } from './_helpers.js';
class AlfDropzone extends HTMLElement {
  connectedCallback() {
    const hint = this.getAttribute('hint') || '';
    const label = this.getAttribute('label') || 'Drop files here or click to browse';
    const compact = this.hasAttribute('compact');
    const multiple = this.hasAttribute('multiple');
    const accept = this.getAttribute('accept') || '';
    this.classList.add('dropzone');
    if (compact) this.classList.add('dropzone-compact');
    this.innerHTML = `<input type="file" style="display:none" ${multiple ? 'multiple' : ''} ${accept ? `accept="${accept}"` : ''}><div class="dropzone-content"><p>${label}</p>${hint ? `<small class="text-dim">${hint}</small>` : ''}</div>`;
    const input = this.querySelector('input');
    this.querySelector('.dropzone-content').addEventListener('click', () => input.click());
    input.addEventListener('change', () => fire(this, 'alf-files', { files: Array.from(input.files) }));
    this.addEventListener('dragover', e => { e.preventDefault(); this.classList.add('dragover'); });
    this.addEventListener('dragleave', () => this.classList.remove('dragover'));
    this.addEventListener('drop', e => { e.preventDefault(); this.classList.remove('dragover'); fire(this, 'alf-files', { files: Array.from(e.dataTransfer.files) }); });
  }
}
customElements.define('alf-dropzone', AlfDropzone);
