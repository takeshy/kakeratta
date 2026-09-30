import test from "node:test";
import assert from "node:assert/strict";
import { t as translate } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createSpeaker } = await import("../frontend/src/speaker.js");
const { api } = await import("../frontend/src/api.js");
class TestNode {
  children = []; listeners = {}; dataset = {}; style = {}; className = ""; value = ""; textContent = "";
  classList = { add() {}, remove() {}, toggle() {} };
  constructor(tag) { this.tag = tag; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  focus() {}
}
const nodes = (root) => [root, ...root.children.flatMap(nodes)];
const hitBy = (id, speaker) => ({ entry: { id, kind: "remark", root: `r${id}`, speaker, at: "2026-09-01T00:00:00Z" }, snippet: `発言${id}` });
// The page comes with the conversations its remarks belong to.
const pageOf = (hits, total) => ({ hits, total, entries: hits.flatMap(({ entry }) => [
 { id: entry.root, kind: "post", root: entry.root, speaker: "user", at: "2026-08-31T00:00:00Z", text: entry.root }, entry]) });
function setup(t, options = {}) {
 globalThis.Node = TestNode;
 globalThis.document = { createElement: tag => new TestNode(tag) };
 t.after(() => { delete globalThis.Node; delete globalThis.document; });
 const view = createSpeaker({ getSettings: () => ({}), getPersona: () => null, ...options });
 return { view, status: nodes(view.element).find(n => n.role === "status"), more: nodes(view.element).find(n => n.textContent === translate("さらに読み込む")) };
}
test("a speaker's list shows what they said a page at a time, each under its post", async t => {
 const { view, status, more } = setup(t); const calls = [];
 const threads = t.mock.method(api, "thread", async () => { throw new Error("one request per page"); });
 t.mock.method(api, "speaker", async (id, opts) => { calls.push([id, opts]); return opts.offset === 0 ? pageOf([hitBy("a", "father"), hitBy("b", "father")], 3) : pageOf([hitBy("c", "father")], 3); });

 await view.show("father", "父");
 assert.deepEqual(calls[0], ["father", { limit: 20, offset: 0 }]);
 assert.equal(threads.mock.callCount(), 0, "no request per conversation");
 assert.ok(status.textContent.includes(translate("{0}件中{1}件を表示", 3, 2)));
 assert.equal(more.hidden, false, "a third is still to come");

 await more.listeners.click();
 assert.deepEqual(calls[1], ["father", { limit: 20, offset: 2 }]);
 assert.equal(more.hidden, true);

 // Another name replaces it; the user is named あなた.
 await view.show("user", "");
 assert.deepEqual(calls[2], ["user", { limit: 20, offset: 0 }]);
});
test("a page that arrives after another speaker was opened is dropped", async t => {
 const { view, more } = setup(t); let finish;
 t.mock.method(api, "speaker", async id => id === "father" ? await new Promise(resolve => { finish = resolve; }) : pageOf([hitBy("z", "mother")], 1));
 const opening = view.show("father", "父");
 await view.show("mother", "母");
 finish(pageOf([hitBy("a", "father")], 5));
 await opening;
 assert.equal(more.hidden, true, "the late page did not bring back its own \"more\"");
});
test("over the list is the persona as its card shows it, with the same ways to open it", async t => {
 const father = { id: "father", name: "父", icon: "👴", kind: "person", domain: "父。", aliases: ["お父さん"], keywords: ["釣り"], basicInfo: "11月10日生まれ。" };
 const opened = [];
 const { view } = setup(t, { getPersona: id => id === "father" ? father : null,
  onOpenPersona: row => opened.push(["persona", row.id]), onOpenMemory: row => opened.push(["memory", row.id]), onEditPersona: (row, focus) => opened.push(["edit", row.id, focus]) });
 t.mock.method(api, "speaker", async () => pageOf([hitBy("a", "father")], 1));
 const host = nodes(view.element).find(n => n.className === "speaker-card-host");
 assert.equal(host.hidden, true, "no card before a name is opened");

 await view.show("father", "父");
 assert.equal(host.hidden, false);
 const card = nodes(host).find(n => n.className.includes("speaker-card"));
 const text = nodes(card).map(n => n.textContent).join("|");
 for (const want of ["父", "父。", translate("呼び名: {0}", "父 / お父さん"), translate("キーワード: {0}", "釣り"), "11月10日生まれ。"]) assert.ok(text.includes(want), `the card shows ${want}`);
 const button = label => nodes(card).find(n => n.textContent === translate(label));
 button("担当の設定").listeners.click();
 button("修正").listeners.click();
 button("覚えていること").listeners.click();
 button("基本情報を編集").listeners.click();
 assert.deepEqual(opened, [["persona", "father"], ["edit", "father", undefined], ["memory", "father"], ["edit", "father", "basicInfo"]]);

 // Changed meanwhile, the card shows it when redrawn.
 father.basicInfo = "11月11日生まれ。";
 view.redrawCard();
 assert.ok(nodes(host).map(n => n.textContent).join("|").includes("11月11日生まれ。"));

 // The user has no card.
 await view.show("user", "");
 assert.equal(host.hidden, true);
});
