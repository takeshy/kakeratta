import test from "node:test";
import assert from "node:assert/strict";
import { setLanguage } from "../frontend/src/i18n.js";
import { api } from "../frontend/src/api.js";
import { openSocial, richText } from "../frontend/src/social.js";

class Element {
  children = []; listeners = {}; textContent = ""; value = ""; disabled = false; checked = false;
  constructor(tag) { this.tag = tag; }
  setAttribute(key, value) {
    if (["checked", "disabled"].includes(key)) this[key] = true;
    else this[key] = value;
  }
  addEventListener(name, fn) { (this.listeners[name] ??= []).push(fn); }
  async fire(name) { for (const fn of this.listeners[name] || []) await fn({ preventDefault() {} }); }
  append(...items) { for (const item of items) { item.parent = this; this.children.push(item); } }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  get firstChild() { return this.children[0]; }
  querySelectorAll(selector) {
    const matches = (node) => selector.split(/, */).some((s) => s.startsWith(".") ? node.className?.split(" ").includes(s.slice(1)) : node.tag === s);
    return this.children.flatMap((node) => [...(matches(node) ? [node] : []), ...node.querySelectorAll(selector)]);
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0]; }
  showModal() { this.open = true; }
  close() { this.open = false; void this.fire("close"); }
}

const settle = async () => { for (let i = 0; i < 4; i++) await new Promise(resolve => setImmediate(resolve)); };
const entries = [
  { id: "p", kind: "post", speaker: "user", text: "麻婆豆腐" },
  { id: "a", root: "p", replyTo: "p", speaker: "tabe", text: "辛そう" },
  { id: "b", root: "p", replyTo: "p", speaker: "gout", text: "private health" },
];
function setup(t, rows = entries) {
  setLanguage("ja");
  globalThis.Node = Element;
  globalThis.document = { body: new Element("body"), createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => {
    for (const dialog of [...document.body.children]) dialog.close();
    delete globalThis.Node; delete globalThis.document;
  });
  t.mock.method(api, "thread", async () => ({ entries: rows }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: true, connected: true, did: "did:plc:alice", handle: "alice.bsky.social" }));
}
function previewPlan(body) {
  return { digest: JSON.stringify(body), handle: "alice.bsky.social", posts: body.entryIds.flatMap(id => {
    const rendered = body.htmlImages?.[id];
    return rendered ? rendered.images.map((image, i) => ({entryId:id, id: i ? `${id}:image:${i+1}` : undefined, text:"［画像］", image})) : [{entryId:id,text:id}];
  }) };
}

test("selection previews automatically and publishes only the displayed request once", async t => {
  setup(t);
  const previews = [];
  t.mock.method(api, "previewSocial", async body => { previews.push(body); return previewPlan(body); });
  let posted, finishPublish, publishCalls = 0;
  t.mock.method(api, "publishSocial", async body => {
    posted = body; publishCalls++;
    await new Promise(resolve => { finishPublish = resolve; });
    return {posts:[{entryId:"p",publication:{url:"https://bsky.app/profile/example/post/root"}}]};
  });
  await openSocial("p", id => ({name:id})); await settle();
  const dialog = document.body.children[0];
  const checks = dialog.querySelectorAll("input");
  assert.deepEqual(checks.map(c => c.checked), [true,false,false]);
  assert.equal(dialog.querySelectorAll("button").some(b => b.textContent === "プレビュー"), false);
  const publish = dialog.querySelector(".social-publish");
  assert.equal(publish.textContent,"投稿");
  assert.equal(publish.disabled,false);
  assert.deepEqual(previews.at(-1).entryIds,["p"]);
  checks[1].checked=true; await checks[1].fire("change");
  assert.deepEqual(previews.at(-1).entryIds,["p","a"]);
  checks[0].checked=false; await checks[0].fire("change");
  assert.deepEqual(previews.at(-1).entryIds,["a"],"answers may be shared alone");
  const displayed=previews.at(-1);
  const sending=publish.fire("click");
  assert.equal(publish.disabled,true);
  assert.equal(checks[0].disabled,true);
  await publish.fire("click");
  assert.equal(publishCalls,1);
  finishPublish(); await sending;
  assert.deepEqual(posted,{...displayed,digest:JSON.stringify(displayed)});
  assert.equal(publish.textContent,"投稿済み");
  assert.equal(publish.disabled,true);
  assert.equal(dialog.querySelector(".social-published-link").querySelectorAll("a").length,1);
  await publish.fire("click"); assert.equal(publishCalls,1);
  checks[1].checked=false; await checks[1].fire("change");
  assert.equal(publish.disabled,true,"nothing selected");
  checks[0].checked=true; await checks[0].fire("change");
  assert.equal(publish.disabled,false,"a new selection can still be published");
});

test("format switches show the actual image pages, then send the chosen format", async t => {
  setup(t);
  let captures=0,posted;
  const previews=[];
  t.mock.method(api,"previewSocial",async body => {previews.push(body);return previewPlan(body);});
  t.mock.method(api,"publishSocial",async body => {posted=body;return {posts:[]};});
  await openSocial("p",id=>({name:id}),()=>({}), {capture:async () => {captures++;return {sourceHash:"hash",images:["data:image/jpeg;base64,page1","data:image/jpeg;base64,page2"],alt:"麻婆豆腐"};}});
  await settle();
  const dialog=document.body.children[0], card=dialog.querySelector(".social-choice");
  const buttons=card.querySelector(".social-formats").querySelectorAll("button");
  await buttons[1].fire("click");
  assert.equal(previews.at(-1).formats.p,"image");
  assert.equal(captures,1);
  assert.equal(card.querySelectorAll("img").length,2);
  assert.equal(buttons[1]["aria-pressed"],"true");
  await buttons[0].fire("click");
  assert.equal(card.querySelectorAll("img").length,0);
  assert.deepEqual(previews.at(-1).htmlImages,{});
  await buttons[1].fire("click"); assert.equal(captures,1,"cached snapshot reused");
  await dialog.querySelector(".social-publish").fire("click");
  assert.equal(posted.formats.p,"image");
  assert.equal(posted.htmlImages.p.images.length,2);
});

test("late preview responses cannot enable publication of an outdated selection", async t => {
  setup(t);
  let first;
  t.mock.method(api,"previewSocial",body => body.entryIds.length===1
    ? new Promise(resolve=>{first=()=>resolve(previewPlan(body));}) : Promise.resolve(previewPlan(body)));
  let posted;
  t.mock.method(api,"publishSocial",async body=>{posted=body;return {posts:[]};});
  await openSocial("p",id=>({name:id})); await settle();
  const dialog=document.body.children[0],publish=dialog.querySelector(".social-publish");
  assert.equal(publish.disabled,true);
  const check=dialog.querySelectorAll("input")[1];check.checked=true;await check.fire("change");
  assert.equal(publish.disabled,false);
  first();await settle();
  await publish.fire("click");assert.deepEqual(posted.entryIds,["p","a"]);
});

test("failed image conversion blocks posting and can be retried or changed to text", async t => {
  setup(t);let draws=0;
  t.mock.method(api,"previewSocial",async body=>previewPlan(body));
  await openSocial("p",id=>({name:id}),()=>({}),{capture:async()=>{if(++draws===1)throw Error("conversion failed");return {images:["image"],sourceHash:"hash"};}});await settle();
  const dialog=document.body.children[0],buttons=dialog.querySelector(".social-formats").querySelectorAll("button");
  await buttons[1].fire("click");
  assert.equal(dialog.querySelector(".social-publish").disabled,true);
  assert.equal(dialog.querySelector(".social-retry").hidden,false);
  await dialog.querySelector(".social-retry").fire("click");
  assert.equal(draws,2);assert.equal(dialog.querySelector(".social-publish").disabled,false);
  await buttons[0].fire("click");assert.equal(dialog.querySelectorAll("img").length,0);
});

test("posted and uncertain publication histories cannot be resent",async t=>{
  setup(t);let state="posted";
  t.mock.method(api,"previewSocial",async body=>({...previewPlan(body),posts:[{entryId:"p",text:"p",publication:{state,url:"https://bsky.app/profile/example/post/a"}}]}));
  await openSocial("p",id=>({name:id}));await settle();
  let dialog=document.body.children[0];
  assert.equal(dialog.querySelector(".social-publish").disabled,true);
  assert.equal(dialog.querySelector(".social-publish").textContent,"投稿済み");
  dialog.close();state="pending";
  await openSocial("p",id=>({name:id}));await settle();dialog=document.body.children[0];
  assert.equal(dialog.querySelector(".social-publish").disabled,true);
});

test("a partial failure refreshes publication history before retrying", async t => {
  setup(t);let partial=false,calls=0;
  t.mock.method(api,"previewSocial",async body=>({...previewPlan(body),posts:body.entryIds.map(id=>({entryId:id,text:id,publication:partial?{state:id==="p"?"posted":"retryable"}:{}}))}));
  t.mock.method(api,"publishSocial",async()=>{calls++;if(calls===1){partial=true;return {error:"try again",posts:[{entryId:"p",publication:{url:"https://bsky.app/profile/example/post/p"}}]};}return {posts:[]};});
  await openSocial("p",id=>({name:id}));await settle();
  const dialog=document.body.children[0],check=dialog.querySelectorAll("input")[1],publish=dialog.querySelector(".social-publish");
  check.checked=true;await check.fire("change");
  await publish.fire("click");
  assert.equal(dialog.querySelectorAll(".tag").length,1,"partial success shown inline");
  assert.equal(publish.disabled,false,"only retryable unposted entries remain");
  await publish.fire("click");assert.equal(calls,2);assert.equal(publish.disabled,true);
});

test("the preview shows link facets on their UTF-8 byte ranges", () => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  try {
    const text = "📊 図からの返信:\n・出典 と https://example.com/x";
    const bytes = value => new TextEncoder().encode(value).length;
    const start = bytes("📊 図からの返信:\n・"), url = bytes("📊 図からの返信:\n・出典 と ");
    const parts = richText({ text, links: [{ start, end: start + bytes("出典"), uri: "https://example.com/s" }, { start: url, end: url + bytes("https://example.com/x"), uri: "https://example.com/x" }] });
    assert.deepEqual(parts.map(part => typeof part === "string" ? part : `<${part.textContent}|${part.href}>`),
      ["📊 図からの返信:\n・", "<出典|https://example.com/s>", " と ", "<https://example.com/x|https://example.com/x>", ""]);
    assert.deepEqual(richText({ text: "plain" }), ["plain"]);
  } finally { delete globalThis.Node; delete globalThis.document; }
});
