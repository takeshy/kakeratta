import test from "node:test";
import assert from "node:assert/strict";
import { t as translate } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createSearch } = await import("../frontend/src/search.js");
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
test('the search form offers voice input next to the query',async t=>{
 const {view}=setup(t);
 const form=nodes(view.element).find(n=>n.className==='timeline-search-form');
 assert.ok(form.children.some(n=>n.className.includes('voice-button')));
 assert.equal(typeof view.stopVoice,'function');
});
test('late meaning results cannot replace a newer keyword query',async t=>{
 const {view,toggle}=setup(t);let finish;
 t.mock.method(api,'searchTimeline',async query=>query.mode==='hybrid'?await new Promise(resolve=>{finish=resolve;}):{hits:[hit(query.query)],total:1});
 await view.searchFor('サザエ');const previous=toggle.listeners.click();
 await view.searchFor('ピノ');finish({hits:[hit('古い結果')],total:1});await previous;
 assert.equal(nodes(view.element).find(n=>n.className==='search-snippet').textContent,'ピノ');
});
const hitBy = (id, speaker) => ({ entry: { id, speaker, at: "2026-09-01T00:00:00Z" }, snippet: `発言${id}` });
test('a speaker\'s list shows what they said a page at a time, and searching again leaves it',async t=>{
 const {view,status}=setup(t);const calls=[];
 t.mock.method(api,'speaker',async (id,opts)=>{calls.push([id,opts]);return opts.offset===0?{hits:[hitBy('a','father'),hitBy('b','father')],total:3}:{hits:[hitBy('c','father')],total:3};});
 t.mock.method(api,'searchTimeline',async ()=>({hits:[hit('サザエ')],total:1}));
 const bar=nodes(view.element).find(n=>n.className.includes('search-speaker'));
 const more=nodes(view.element).find(n=>n.textContent===translate('さらに読み込む'));
 assert.equal(bar.hidden,true,'no bar before a name is opened');

 await view.showSpeaker('father','父');
 assert.deepEqual(calls[0],['father',{limit:20,offset:0}]);
 assert.equal(bar.hidden,false);
 assert.ok(nodes(bar).some(n=>n.textContent===translate('{0} の発言','父')));
 assert.equal(nodes(view.element).filter(n=>n.className==='search-hit').length,2);
 assert.ok(status.textContent.includes(translate('{0}件中{1}件を表示',3,2)));
 assert.equal(more.hidden,false,'a third is still to come');

 await more.listeners.click();
 assert.deepEqual(calls[1],['father',{limit:20,offset:2}]);
 assert.equal(nodes(view.element).filter(n=>n.className==='search-hit').length,3);
 assert.equal(more.hidden,true);

 // Another name replaces it; a search leaves it.
 await view.showSpeaker('user','');
 assert.deepEqual(calls[2],['user',{limit:20,offset:0}]);
 assert.ok(nodes(bar).some(n=>n.textContent===translate('{0} の発言',translate('あなた'))),'the user is named あなた');
 await view.searchFor('サザエ');
 assert.equal(bar.hidden,true);
 assert.equal(nodes(view.element).filter(n=>n.className==='search-hit').length,1);
});
test('leaving a speaker\'s list empties it, and a late page cannot bring it back',async t=>{
 const {view}=setup(t);let finish;
 t.mock.method(api,'speaker',async ()=>await new Promise(resolve=>{finish=resolve;}));
 const bar=nodes(view.element).find(n=>n.className.includes('search-speaker'));
 const opening=view.showSpeaker('father','父');
 const back=nodes(bar).find(n=>n.textContent===translate('検索に戻る'));
 back.listeners.click();
 finish({hits:[hitBy('a','father')],total:1});await opening;
 assert.equal(bar.hidden,true);
 assert.equal(nodes(view.element).filter(n=>n.className==='search-hit').length,0);
});
test('over a speaker\'s list is the persona as its card shows it, with the same ways to open it',async t=>{
 globalThis.Node=TestNode;globalThis.document={createElement:tag=>new TestNode(tag)};
 t.after(()=>{delete globalThis.Node;delete globalThis.document;});
 const father={id:'father',name:'父',icon:'👴',kind:'person',domain:'父。',aliases:['お父さん'],keywords:['釣り'],basicInfo:'11月10日生まれ。'};
 const opened=[];
 const view=createSearch({getSettings:()=>({}),getPersona:id=>id==='father'?father:null,
  onOpenPersona:row=>opened.push(['persona',row.id]),onOpenMemory:row=>opened.push(['memory',row.id]),onEditPersona:(row,focus)=>opened.push(['edit',row.id,focus])});
 t.mock.method(api,'speaker',async ()=>({hits:[hitBy('a','father')],total:1}));
 const host=nodes(view.element).find(n=>n.className==='speaker-card-host');
 assert.equal(host.hidden,true,'no card before a name is opened');

 await view.showSpeaker('father','父');
 assert.equal(host.hidden,false);
 const card=nodes(host).find(n=>n.className.includes('speaker-card'));
 const text=nodes(card).map(n=>n.textContent).join('|');
 for(const want of ['父','父。',translate('呼び名: {0}','父 / お父さん'),translate('キーワード: {0}','釣り'),'11月10日生まれ。'])assert.ok(text.includes(want),`the card shows ${want}`);
 const button=label=>nodes(card).find(n=>n.textContent===translate(label));
 button('担当の設定').listeners.click();
 button('修正').listeners.click();
 button('覚えていること').listeners.click();
 button('基本情報を編集').listeners.click();
 assert.deepEqual(opened,[['persona','father'],['edit','father',undefined],['memory','father'],['edit','father','basicInfo']]);

 // Changed meanwhile, the card shows it when redrawn.
 father.basicInfo='11月11日生まれ。';
 view.redrawSpeakerCard();
 assert.ok(nodes(host).map(n=>n.textContent).join('|').includes('11月11日生まれ。'));

 // The user has no card, and leaving the list takes it away.
 await view.showSpeaker('user','');
 assert.equal(host.hidden,true);
 await view.showSpeaker('father','父');
 nodes(view.element).find(n=>n.textContent===translate('検索に戻る')).listeners.click();
 assert.equal(host.hidden,true);
});
