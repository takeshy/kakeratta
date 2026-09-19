import test from "node:test";
import assert from "node:assert/strict";
import { aiConfigured, createSettings } from "../frontend/src/settings.js";
import { api } from "../frontend/src/api.js";
import { t as translate } from "../frontend/src/i18n.js";

test("remote AI setup requires an endpoint, low model, and API key", () => {
  const ready = { provider: "nebius", baseUrl: "https://api.example/v1", lowModel: "small", apiKey: "********" };
  assert.equal(aiConfigured(ready), true);
  assert.equal(aiConfigured({ ...ready, apiKey: "" }), false);
  assert.equal(aiConfigured({ ...ready, lowModel: "" }), false);
  assert.equal(aiConfigured({ ...ready, baseUrl: "" }), false);
});

class Element {
  children = []; listeners = {}; textContent = ""; className = ""; style = {}; value = "";
  classList = { add() {}, remove() {}, toggle() {} };
  constructor(tag) { this.tag = tag; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  replaceChildren(...nodes) { this.children = []; this.append(...nodes); }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  get firstChild() { return this.children[0]; }
  setAttribute(key, value) { this[key] = key === "disabled" || key === "checked" ? true : value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
  focus() {}
}
const walk = node => [node, ...node.children.flatMap(walk)];

// The page asks for a backup and waits for a link: on a laptop the answer is
// the streaming endpoint, and a hosted workspace answers later over the event
// stream with a link to storage.
test("the backup is offered as a link once the server has one", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "about", async () => ({ version: "test", workspace: "/tmp/kakeratta" }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "startExport", async () => ({ state: "ready", url: "/api/export", name: "kakeratta-test.zip" }));

  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  assert.ok(walk(view.element).some(node => node.textContent === translate("バックアップ")));
  const start = walk(view.element).find(node => node.tag === "button" && node.textContent === translate("zipに書き出す"));
  assert.ok(start, "the backup button is missing");

  start.listeners.click();
  await new Promise(resolve => setImmediate(resolve));
  const link = walk(view.element).find(node => node.tag === "a" && node.href === "/api/export");
  assert.ok(link, "the download link is missing");
  assert.equal(link.textContent, translate("ダウンロード"));
  assert.equal(link.download, "kakeratta-test.zip");
});

// A hosted workspace writes the backup to storage, which takes as long as it
// takes; the link arrives on the event stream the page is already listening to.
test("a backup prepared in storage is offered when its link arrives", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "about", async () => ({ version: "test", workspace: "/tmp/kakeratta" }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "startExport", async () => ({ state: "running", name: "kakeratta-test.zip" }));
  let announce = () => {};
  t.mock.method(api, "on", listener => { announce = listener; return () => { announce = () => {}; }; });

  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  const start = walk(view.element).find(node => node.tag === "button" && node.textContent === translate("zipに書き出す"));
  start.listeners.click();
  await new Promise(resolve => setImmediate(resolve));

  const waiting = walk(view.element).find(node => node.tag === "button" && node.textContent === translate("書き出し中…"));
  assert.ok(waiting, "the button should say it is working");
  assert.equal(waiting.disabled, true);
  assert.equal(walk(view.element).some(node => node.tag === "a" && node.download !== undefined), false);

  announce("export", { state: "ready", url: "https://storage.example/kakeratta-test.zip?signed", name: "kakeratta-test.zip" });
  const link = walk(view.element).find(node => node.tag === "a" && node.href?.startsWith("https://storage.example/"));
  assert.ok(link, "the download link is missing");
  assert.equal(link.textContent, translate("ダウンロード"));
});
