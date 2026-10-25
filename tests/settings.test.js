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
  get parentElement() { return this.parent; }
  setAttribute(key, value) { this[key] = key === "disabled" || key === "checked" ? true : value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
  focus() { this.focused = true; }
  scrollIntoView() { this.scrolled = true; }
  querySelector(tag) { return walk(this).find(node => node.tag === tag); }
}
const walk = node => [node, ...node.children.flatMap(walk)];
// Opens every group on the panel, as a reader tapping each row would.
function openGroups(view) {
  const groups = walk(view.element).filter(node => node.tag === "details" && node["data-category"]);
  for (const group of groups) {
    group.open = true;
    group.listeners.toggle();
  }
}

// The page asks for a backup and waits for a link: on a laptop the answer is
// the streaming endpoint, and a hosted workspace answers later over the event
// stream with a link to storage.
test("the backup is offered as a link once the server has one", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "startExport", async () => ({ state: "ready", url: "/api/export", name: "kakeratta-test.zip" }));

  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openGroups(view);
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
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "startExport", async () => ({ state: "running", name: "kakeratta-test.zip" }));
  let announce = () => {};
  t.mock.method(api, "on", listener => { announce = listener; return () => { announce = () => {}; }; });

  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openGroups(view);
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
function panel(t) {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  return createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
}

const heading = (view, label) => walk(view.element).some(node => ["h3", "summary"].includes(node.tag) && node.textContent === translate(label));

// What the defaults already answer waits in groups, each shut and not even
// built - so a panel that opens does not ask Hermes or Bluesky anything, and
// there is no "advanced" section to look behind.
test("speech, OpenRouter, Hermes and Bluesky wait in shut groups", async t => {
  const view = panel(t);
  await view.load();
  for (const label of ["モデル", "Web検索", "この端末", "OpenRouterを使う", "入力", "音声", "外部連携", "バックアップ"]) {
    assert.ok(heading(view, label), `${label} should be in plain sight`);
  }
  for (const label of ["音声入力", "読み上げ", "Bluesky連携"]) {
    assert.equal(heading(view, label), false, `${label} should wait in its group`);
  }
  assert.equal(walk(view.element).some(node => node.tag === "button" && node.textContent === translate("zipに書き出す")), false);
  assert.equal(walk(view.element).some(node => String(node.textContent).includes("上級オプション")), false);
  assert.equal(walk(view.element).some(node => String(node.textContent).startsWith("kakeratta ")), false, "no version line");
  assert.equal(api.mcpConnection.mock.callCount(), 0, "a shut group should ask nothing");
  assert.equal(api.blueskyConnection.mock.callCount(), 0, "a shut group should ask nothing");

  openGroups(view);
  for (const label of ["音声入力", "読み上げ", "Bluesky連携"]) {
    assert.ok(heading(view, label), `${label} should appear once its group is opened`);
  }
  assert.equal(api.mcpConnection.mock.callCount(), 1);
  assert.equal(api.blueskyConnection.mock.callCount(), 1);
});

test("the external agent switch sits under its heading, above the connections", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  globalThis.location = { origin: "https://kakeratta.example" };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {}, externalAgent: { paused: false } }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: true, path: "/api/mcp/users/sample", connections: [{ id: "hermes", name: "Hermes", active: true }] }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openGroups(view);
  await new Promise(setImmediate);
  const nodes = walk(view.element);
  const at = text => nodes.findIndex(node => node.textContent === translate(text));
  assert.ok(at("外部エージェント接続") < at("外部エージェントを使う"));
  assert.ok(at("外部エージェントを使う") < at("使用する接続先"));
  assert.ok(at("使用する接続先") < at("＋ 接続先を追加"));
});

// Leaving is asked for in a word, done on the server, and finished by signing
// out of this browser. Without an account there is nothing to close, so the
// section is not there.
test("closing the account asks for the word, deletes on the server, and signs out", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.prompt; });
  t.mock.method(api, "settings", async () => ({ baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {} }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "closeAccount", async () => null);
  let signedOut = 0;
  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {}, closeAccount: async () => { signedOut++; } });
  await view.load();
  openGroups(view);
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
  openGroups(local);
  assert.equal(heading(local, "利用停止"), false, "without an account there is nothing to close");
});

test("OpenRouter setup needs its own key, not a Token Factory key", () => {
  assert.equal(aiConfigured({ openRouterMode: true, openRouterApiKey: "********", apiKey: "" }), true);
  assert.equal(aiConfigured({ openRouterMode: true, openRouterApiKey: "  ", apiKey: "nebius", baseUrl: "https://example.com", lowModel: "low" }), false);
  assert.equal(aiConfigured({ openRouterMode: false, openRouterApiKey: "********", jevEnabled: true }), false);
});

test("OpenRouter can be toggled off and on with saved settings without reconnecting", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let saved = { baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", openRouterApiKey: "********", openRouterRouterModel: "~typesafe/jev-latest", openRouterHighModel: "high", openRouterMiddleModel: "middle", openRouterLowModel: "low", openRouterMode: true, speech: {}, search: {} };
  t.mock.method(api, "settings", async () => saved);
  t.mock.method(api, "saveSettings", async next => { saved = next; return next; });
  t.mock.method(api, "mcpConnection", async () => ({ configured: false }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openGroups(view);

  const toggle = label => walk(view.element).find(node => node.tag === "label" && node.children.some(child => child.textContent === translate(label))).querySelector("input");
  const openRouter = toggle("OpenRouterを使う");
  assert.equal(openRouter.disabled, false);

  openRouter.checked = false;
  openRouter.listeners.change();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(saved.openRouterMode, false);
  assert.equal(saved.openRouterApiKey, "********");
  assert.equal(saved.openRouterRouterModel, "~typesafe/jev-latest");
  assert.equal(saved.openRouterHighModel, "high");
  assert.equal(saved.openRouterMiddleModel, "middle");
  assert.equal(saved.openRouterLowModel, "low");
  const reopened = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await reopened.load();
  openGroups(reopened);
  const restored = walk(reopened.element).find(node => node.tag === "label" && node.children.some(child => child.textContent === translate("OpenRouterを使う"))).querySelector("input");
  assert.equal(restored.disabled, false);
  restored.checked = true;
  restored.listeners.change();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(saved.openRouterMode, true);
  assert.equal(saved.openRouterRouterModel, "~typesafe/jev-latest");
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


test("external agent refresh keeps its settings switch mounted", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  globalThis.location = { origin: "https://kakeratta.net" };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; });
  let current = { baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", speech: {}, search: {}, externalAgent: { paused: false } };
  t.mock.method(api, "settings", async () => current);
  t.mock.method(api, "saveSettings", async next => { current = next; return next; });
  t.mock.method(api, "blueskyConnection", async () => ({ available: false }));
  t.mock.method(api, "mcpConnection", async () => ({ configured: true, path: "/api/mcp", connections: [], agent: { paused: current.externalAgent.paused } }));
  const panel = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] } });
  await panel.load();
  openGroups(panel);
  await new Promise(setImmediate);
  const controls = walk(panel.element).find(node => node.className === "settings-block external-agent");
  const parent = controls.parent;
  const toggle = controls.querySelector("input");
  panel.element.scrollTop = 600;
  toggle.checked = false;
  await toggle.listeners.change();
  await new Promise(setImmediate);
  assert.equal(controls.parent, parent, "refresh must not detach the switch's container");
  assert.equal(controls.querySelector("input"), toggle);
  assert.equal(panel.element.scrollTop, 600);
  assert.equal(current.externalAgent.paused, true);
});


test("profile settings save a display name and icon without changing model configuration", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let current = { baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "********", speech: {}, search: {}, profile: { name: "以前の名前", icon: "🙂" } };
  t.mock.method(api, "settings", async () => current);
  t.mock.method(api, "saveSettings", async next => { current = next; return next; });
  const view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] } });
  await view.load();
  const name = walk(view.element).find(node => node.tag === "label" && node.children.some(child => child.textContent === translate("表示名"))).querySelector("input");
  name.value = "佐藤";
  await name.listeners.change();
  await new Promise(setImmediate);
  assert.equal(current.profile.name, "佐藤");
  const icon = walk(view.element).find(node => node.className === "avatar-icon-input");
  icon.value = "🌸";
  await icon.listeners.change();
  assert.deepEqual(current.profile, { name: "佐藤", icon: "🌸", avatar: "" });
  assert.equal(current.apiKey, "********");
  assert.equal(current.baseUrl, "https://api.example/v1");
});

test("OpenRouter image analysis model loads, saves, and survives provider toggles", async t => {
  let view = panel(t);
  document.querySelector = () => null;
  let saved = { baseUrl: "https://api.example/v1", lowModel: "low", apiKey: "key", visionModel: "main-vision", openRouterApiKey: "********", openRouterMode: true, speech: {}, search: {} };
  t.mock.method(api, "settings", async () => saved);
  t.mock.method(api, "saveSettings", async next => { saved = next; return next; });
  t.mock.method(api, "openRouterModels", async () => ({ models: ["provider/vision-a", "provider/vision-b"] }));
  await view.load();
  openGroups(view);
  const visionPicker = () => walk(view.element).find(node => node.className === "field" && node.children.some(child => child.textContent === translate("画像解析モデル")));
  assert.ok(walk(visionPicker()).some(node => node.textContent === "z-ai/glm-5.3-flash"));
  const check = walk(view.element).find(node => node.tag === "button" && node.textContent === translate("OpenRouterの接続確認"));
  await check.listeners.click();
  const selected = walk(visionPicker()).find(node => node.tag === "button" && node.textContent === "provider/vision-b");
  assert.ok(selected, "connection check must populate the image model picker");
  selected.listeners.click();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(saved.openRouterVisionModel, "provider/vision-b");
  assert.equal(saved.visionModel, "main-vision");
  const toggle = walk(view.element).find(node => node.tag === "label" && node.children.some(child => child.textContent === translate("OpenRouterを使う"))).querySelector("input");
  for (const enabled of [false, true]) {
    toggle.checked = enabled;
    await toggle.listeners.change();
    assert.equal(saved.openRouterVisionModel, "provider/vision-b");
  }
  view = createSettings({ speaker: { setRate() {}, setVoice() {}, voices: () => [] }, onSettings() {} });
  await view.load();
  openGroups(view);
  assert.ok(walk(visionPicker()).some(node => node.textContent === "provider/vision-b"));
});
