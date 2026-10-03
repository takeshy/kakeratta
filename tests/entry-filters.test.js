import test from "node:test";
import assert from "node:assert/strict";
globalThis.EventSource = class { close() {} };
const {createEntryFilters,filterConversations}=await import("../frontend/src/entry-filters.js");
const {adoptFavoriteIDs}=await import("../frontend/src/favorite-state.js");
class NodeStub {
 children=[];listeners={};value="";style={};
 append(...nodes){this.children.push(...nodes)}
 setAttribute(k,v){this[k]=v}
 addEventListener(k,v){this.listeners[k]=v}
}
const all=n=>[n,...n.children.flatMap(all)];
test("dates include the whole local end day, reject reversed dates, and clear together", async t=>{
 globalThis.Node=NodeStub;
 globalThis.document={createElement:tag=>Object.assign(new NodeStub(),{tag}),createTextNode:text=>Object.assign(new NodeStub(),{textContent:text})};
 t.after(()=>{delete globalThis.Node;delete globalThis.document;});
 const calls=[],view=createEntryFilters(q=>calls.push(q)),nodes=all(view.element);
 assert.equal(view.element.tag,"details");
 assert.ok(!view.element.open);
 const status=nodes.find(n=>n.className==="entry-filter-status");
 const [from,to]=nodes.filter(n=>n.type==="date");
 const favorite=nodes.find(n=>n.type==="checkbox");
 favorite.checked=true;from.value="2026-10-01";to.value="2026-10-02";to.listeners.change();
 assert.equal(calls[0].from,new Date("2026-10-01T00:00:00").toISOString());
 assert.equal(calls[0].to,new Date(new Date("2026-10-03T00:00:00").getTime()-1).toISOString());
 assert.equal(calls[0].favoritesOnly,true);
 assert.ok(status.textContent);
 view.element.open=false;
 assert.deepEqual(view.value(),calls[0]);
 from.value="2026-10-03";from.listeners.change();
 assert.equal(calls.length,1);
 assert.ok(nodes.find(n=>n.role==="alert").textContent);
 nodes.find(n=>n.tag==="button").listeners.click();
 assert.deepEqual(calls.at(-1),{});
 assert.equal(status.textContent,"");
});
test("favorite replies retain parent context and date filtering uses the saved reply date",()=>{
 globalThis.document={querySelectorAll:()=>[]};
 adoptFavoriteIDs(["reply"]);
 const entries=[
 {id:"old",kind:"post",at:"2026-09-01T00:00:00Z"},
 {id:"reply",root:"old",kind:"remark",at:"2026-10-01T12:00:00Z"},
 {id:"other",kind:"post",at:"2026-10-01T00:00:00Z"},
 ];
 const filter={from:"2026-10-01T00:00:00Z",to:"2026-10-01T23:59:59.999Z"};
 assert.deepEqual(filterConversations(entries,filter).map(e=>e.id),["other"]);
 assert.deepEqual(filterConversations(entries,{...filter,favoritesOnly:true}).map(e=>e.id),["old","reply"]);
 delete globalThis.document;
});
