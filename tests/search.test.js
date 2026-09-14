import test from "node:test";
import assert from "node:assert/strict";
import { t as translate } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createSearch } = await import("../frontend/src/search.js");
const { api } = await import("../frontend/src/api.js");
class TestNode {
  children = []; listeners = {}; dataset = {}; style = {}; className = ""; value = ""; textContent = "";
  constructor(tag) { this.tag = tag; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  setAttribute(name, value) { this[name] = value; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  focus() {}
}
const nodes = (root) => [root, ...root.children.flatMap(nodes)];
const hit = id => ({ entry: { id, speaker: "user", at: "2026-09-01T00:00:00Z" }, snippet: id });
function setup(t) {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: tag => new TestNode(tag) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const view = createSearch({ getSettings: () => ({}), getPersona: () => null });
  return {view, toggle:nodes(view.element).find(n=>n.className.includes('search-expand')), status:nodes(view.element).find(n=>n.role==='status')};
}
test('keywords return first; expanding runs meaning search and preserves visible results while waiting',async t=>{
 const {view,toggle}=setup(t);const queries=[];let finish;
 t.mock.method(api,'searchTimeline',async query=>{queries.push(query);return query.mode==='keyword'?{hits:[hit('サザエ')],total:1}:await new Promise(resolve=>{finish=resolve;});});
 await view.searchFor('サザエ');
 assert.deepEqual(queries,[{query:'サザエ',mode:'keyword',limit:20,offset:0}]);
 assert.equal(toggle.hidden,false);assert.equal(toggle.textContent,translate('似たような単語でも探す'));
 const expanded=toggle.listeners.click();
 assert.equal(nodes(view.element).filter(n=>n.className==='search-hit').length,1);
 finish({hits:[hit('サザエ'),hit('貝料理')],total:2});await expanded;
 assert.equal(queries[1].mode,'hybrid');assert.equal(queries[1].offset,0);
 assert.equal(toggle.textContent,translate('キーワードだけで探す'));
});
test('sentences start by meaning; users can switch even when no results exist',async t=>{
 const {view,toggle,status}=setup(t);const queries=[];
 t.mock.method(api,'searchTimeline',async query=>{queries.push(query);return {hits:[],total:0};});
 await view.searchFor('昨日食べた貝を探して');
 assert.equal(queries[0].mode,'hybrid');assert.equal(toggle.hidden,false);
 assert.ok(status.textContent.includes(translate('一致するかけらがありません')));
 await toggle.listeners.click();assert.equal(queries[1].mode,'keyword');
 assert.equal(toggle.hidden,false);assert.equal(toggle.textContent,translate('似たような単語でも探す'));
});
test('meaning search fallback offers retry and a new query forgets the override',async t=>{
 const {view,toggle,status}=setup(t);const queries=[];
 t.mock.method(api,'searchTimeline',async query=>{queries.push(query);return {hits:[],total:0,semanticUnavailable:query.mode==='hybrid'};});
 await view.searchFor('ワインを教えて');
 assert.ok(status.textContent.includes(translate('意味検索を利用できなかったため、語句の一致で探しました。あとでもう一度検索できます。')));
 assert.equal(toggle.textContent,translate('似たような単語でも探す'));
 await view.searchFor('ピノ');assert.equal(queries.at(-1).mode,'keyword');
});
test('late meaning results cannot replace a newer keyword query',async t=>{
 const {view,toggle}=setup(t);let finish;
 t.mock.method(api,'searchTimeline',async query=>query.mode==='hybrid'?await new Promise(resolve=>{finish=resolve;}):{hits:[hit(query.query)],total:1});
 await view.searchFor('サザエ');const previous=toggle.listeners.click();
 await view.searchFor('ピノ');finish({hits:[hit('古い結果')],total:1});await previous;
 assert.equal(nodes(view.element).find(n=>n.className==='search-snippet').textContent,'ピノ');
});
