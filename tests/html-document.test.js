import test from "node:test";
import assert from "node:assert/strict";
import { htmlDocument, renderHtmlDocument } from "../frontend/src/html-document.js";

test("only complete HTML answers become documents, including a single HTML fence", () => {
  const html = '<!DOCTYPE html><html lang="en"><body><h1>Sales</h1></body></html>';
  assert.equal(htmlDocument(html), html);
  assert.equal(htmlDocument('```html\n' + html + '\n```'), html);
  for (const text of ['<div>example</div>', 'Here is <html>example</html>', '```html\n<div>code sample</div>\n```', '<html>incomplete']) assert.equal(htmlDocument(text), null);
});

test("HTML answers use an isolated frame with external resources and scripts blocked", t => {
  globalThis.document = { createElement: tag => ({ tag, setAttribute(name, value) { this[name] = value; } }) };
  t.after(() => { delete globalThis.document; });
  const source = '<html><body><script>parent.document.body.remove()</script><img src="https://evil.example/track"><h1>Sales</h1></body></html>';
  const frame = renderHtmlDocument(source);
  assert.equal(frame.tag, 'iframe');
  assert.equal(frame.sandbox, '');
  assert.equal(frame.referrerpolicy, 'no-referrer');
  assert.ok(frame.srcdoc.indexOf('Content-Security-Policy') < frame.srcdoc.indexOf('<script>'));
  assert.match(frame.srcdoc, /default-src 'none'/);
  assert.match(frame.srcdoc, /style-src 'unsafe-inline'/);
  assert.match(frame.srcdoc, /img-src data:/);
  assert.ok(frame.srcdoc.endsWith(source));
});
