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
  focus() { this.focused = true; }
  scrollIntoView() { this.scrolled = true; }
  querySelector(tag) { return walk(this).find(node => node.tag === tag); }
}
const walk = node => [node, ...node.children.flatMap(walk)];
function openAdvanced(view) {
  const section = walk(view.element).find(node => node.tag === "details");
  section.open = true;
  section.listeners.toggle();
}

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
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "startExport", async () => ({ state: "ready", url: "/api/export", name: "kakeratta-test.zip" }));

  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openAdvanced(view);
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
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "startExport", async () => ({ state: "running", name: "kakeratta-test.zip" }));
  let announce = () => {};
  t.mock.method(api, "on", listener => { announce = listener; return () => { announce = () => {}; }; });

  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openAdvanced(view);
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

// A fresh panel with every call the page makes on load stubbed out.
function panel(t, { about: info = { version: "test" } } = {}) {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "about", async () => info);
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  return createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
}

const heading = (view, label) => walk(view.element).some(node => ["h3", "summary"].includes(node.tag) && node.textContent === translate(label));

// What the defaults already answer is behind one line, and it is not even
// built - so a panel that opens does not ask Hermes or Bluesky anything.
test("speech, OpenRouter, Hermes and Bluesky wait behind the advanced line", async t => {
  const view = panel(t);
  await view.load();
  for (const label of ["モデル", "Web検索", "この端末"]) {
    assert.ok(heading(view, label), `${label} should be in plain sight`);
  }
  for (const label of ["OpenRouterを使う", "音声入力", "読み上げ", "Bluesky連携", "バックアップ"]) {
    assert.equal(heading(view, label), false, `${label} should be behind the line`);
  }
  assert.equal(api.mcpConnection.mock.callCount(), 0, "a shut section should ask nothing");
  assert.equal(api.blueskyConnection.mock.callCount(), 0, "a shut section should ask nothing");

  const advanced = walk(view.element).find(node => node.tag === "details" && node.className === "settings-advanced");
  assert.ok(advanced, "the advanced section is missing");
  assert.equal(advanced.firstChild.textContent, translate("上級オプション"));
  advanced.open = true;
  advanced.listeners.toggle();
  for (const label of ["OpenRouterを使う", "音声入力", "読み上げ", "Bluesky連携", "バックアップ"]) {
    assert.ok(heading(view, label), `${label} should appear once the section is opened`);
  }
  assert.equal(api.mcpConnection.mock.callCount(), 1);
  assert.equal(api.blueskyConnection.mock.callCount(), 1);
});

// Leaving is asked for in a word, done on the server, and finished by signing
// out of this browser. Without an account there is nothing to close, so the
// section is not there.
test("closing the account asks for the word, deletes on the server, and signs out", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.prompt; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "about", async () => ({ version: "test" }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "closeAccount", async () => null);
  let signedOut = 0;
  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {}, closeAccount: async () => { signedOut++; } });
  await view.load();
  openAdvanced(view);
  assert.ok(heading(view, "利用停止"), "the closing section is missing");
  const button = () => walk(view.element).find(node => node.tag === "button" && node.textContent === translate("すべて削除して利用を停止する"));
  assert.ok(button(), "the closing button is missing");

  globalThis.prompt = () => "";
  button().listeners.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(api.closeAccount.mock.callCount(), 0, "a wrong word closes nothing");
  assert.equal(signedOut, 0);

  globalThis.prompt = () => ` ${translate("削除")} `;
  button().listeners.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(api.closeAccount.mock.callCount(), 1);
  assert.equal(signedOut, 1);

  const local = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await local.load();
  openAdvanced(local);
  assert.equal(heading(local, "利用停止"), false, "without an account there is nothing to close");
});

// The path is the server's answer, not the page's guess: a hosted workspace
// keeps its files in Firestore and sends none.
test("the version is shown without a workspace path", async t => {
  const hosted = panel(t);
  await hosted.load();
  assert.equal(walk(hosted.element).some(node => String(node.textContent).includes("kakeratta test")), true);
  assert.equal(walk(hosted.element).some(node => String(node.textContent).startsWith(translate("保存先 {0}", "").trim())), false);

  const local = panel(t, { about: { version: "test", workspace: "~/.config/kakeratta" } });
  await local.load();
  assert.equal(walk(local.element).some(node => node.textContent.includes("~/.config/kakeratta")), false);
});


test("OpenRouter-only setup needs its own key, not a Token Factory key", () => {
  assert.equal(aiConfigured({ openRouterMode: true, openRouterApiKey: "********", apiKey: "" }), true);
  assert.equal(aiConfigured({ openRouterMode: true, openRouterApiKey: "  ", apiKey: "nebius", baseUrl: "https://example.com", lowModel: "low" }), false);
  assert.equal(aiConfigured({ openRouterMode: false, openRouterApiKey: "********", jevEnabled: true }), false);
});

test("initial setup link opens and focuses the OpenRouter settings", async t => {
  const view = panel(t);
  t.mock.method(api, "settings", async () => ({ apiKey: "", speech: {}, search: {} }));
  await view.load();
  const link = walk(view.element).find(node => node.href === "#openrouter-settings");
  assert.ok(link);
  link.listeners.click({ preventDefault() {} });
  const section = walk(view.element).find(node => node.id === "openrouter-settings");
  assert.ok(section.scrolled);
  assert.ok(section.querySelector("input").focused);
  assert.ok(heading(view, "バックアップ"));
});
