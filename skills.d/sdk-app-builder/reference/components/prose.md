# Prose / Markdown

Wrapper for rendered markdown/HTML content. Styles headings, lists, code, tables, blockquotes.

```html
<div class="prose">
  <h1>Heading 1</h1>
  <h2>Heading 2</h2>
  <h3>Heading 3</h3>
  <p>Paragraph with <strong>bold</strong>, <em>italic</em>, <a href="#">links</a>, and <code>inline code</code>.</p>
  <ul>
    <li>Unordered item</li>
  </ul>
  <ol>
    <li>Ordered item</li>
  </ol>
  <blockquote>Blockquote for callouts.</blockquote>
  <pre><code>const x = 'code block'</code></pre>
  <table>
    <thead><tr><th>Name</th><th>Type</th></tr></thead>
    <tbody><tr><td>variant</td><td>string</td></tr></tbody>
  </table>
</div>
```

Works inside accordion:
```html
<div class="accordion-content prose">
  <!-- markdown HTML here -->
</div>
```
