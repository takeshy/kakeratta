import test from "node:test";
import assert from "node:assert/strict";
import { t as translate } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createSearch } = await import("../frontend/src/search.js");
const { api } = await import("../frontend/src/api.js");

class TestNode {
  children = [];
  listeners = {};
  dataset = {};
  style = {};
  className = "";
  value = "";
  textContent = "";
  constructor(tag) { this.tag = tag; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  focus() {}
}
const nodes = (root) => [root, ...root.children.flatMap(nodes)];

test("one search field sends no mode and explains fallback even with no hits", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: tag => new TestNode(tag) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const queries = [];
  let empty = false;
  t.mock.method(api, "searchTimeline", async query => {
    queries.push(query);
    return { hits: empty ? [] : [{ entry: { id: "p", speaker: "user", at: "2026-09-01T00:00:00Z" }, snippet: "ピノ" }], total: empty ? 0 : 1, semanticUnavailable: true };
  });
  const view = createSearch({ getSettings: () => ({}), getPersona: () => null });
  assert.equal(nodes(view.element).some(n => n.tag === "select"), false);
  await view.searchFor("ピノ");
  assert.deepEqual(queries[0], { query: "ピノ", limit: 20, offset: 0 });
  assert.equal(nodes(view.element).filter(n => n.className === "search-hit").length, 1);
  const status = nodes(view.element).find(n => n.role === "status");
  assert.ok(status.textContent.includes(translate("意味検索を利用できなかったため、語句の一致で探しました。あとでもう一度検索できます。")));
  empty = true;
  await view.searchFor("見つからない記録");
  assert.equal(nodes(view.element).filter(n => n.className === "search-hit").length, 0);
  assert.ok(status.textContent.includes(translate("一致するかけらがありません")));
  assert.ok(status.textContent.includes(translate("意味検索を利用できなかったため、語句の一致で探しました。あとでもう一度検索できます。")));
});
