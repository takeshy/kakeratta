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

test("only chosen replies reach preview and publish; changing selection invalidates approval", async (t) => {
  setLanguage("ja");
  globalThis.Node = Element;
  globalThis.document = { body: new Element("body"), createElement: (tag) => new Element(tag), createTextNode: (text) => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api, "thread", async () => ({ entries: [
    { id: "p", kind: "post", speaker: "user", text: "麻婆豆腐" },
    { id: "a", root: "p", replyTo: "p", speaker: "tabe", text: "辛そう" },
    { id: "b", root: "p", replyTo: "p", speaker: "gout", text: "private health" },
  ] }));
  t.mock.method(api, "blueskyConnection", async () => ({ available: true, connected: true, did: "did:plc:alice", handle: "alice.bsky.social" }));
  let chosen, posted, finishPublish, publishCalls = 0;
  t.mock.method(api, "previewSocial", async (body) => {
    chosen = body;
    return { digest: "digest", posts: body.entryIds.flatMap((id) => id === "p" ? [{ entryId: id, text: id }, { entryId: id, id: "p:part:2", text: "つづき:\ncontinued", parent: "p" }] : [{ entryId: id, text: id, parent: "p:part:2" }]) };
  });
  t.mock.method(api, "publishSocial", async (body) => { posted = body; publishCalls++; await new Promise(resolve => { finishPublish = resolve; }); return { error: "", posts: [{entryId:"p", publication:{url:"https://bsky.app/profile/example/post/root"}}, {entryId:"p", publication:{url:"https://bsky.app/profile/example/post/continued"}}, {entryId:"a", publication:{url:"https://bsky.app/profile/example/post/reply"}}] }; });
  await openSocial("p", (id) => ({ name: id, icon: "🍚" }));
  const dialog = document.body.children[0];
  const checkboxes = dialog.querySelector(".social-selection").querySelectorAll("input");
  assert.deepEqual(checkboxes.map((c) => c.checked), [true, false, false]);
  assert.equal(checkboxes[0].disabled, false, "original post may be deselected");
  const buttons = dialog.querySelectorAll("button");
  const preview = buttons.find((b) => b.textContent === "プレビュー");
  const publish = buttons.find((b) => b.textContent === "選択した会話を投稿");
  assert.equal(publish.disabled, true);
  assert.equal(publish.hidden, true, "publish is not offered before preview");
  checkboxes[1].checked = true; await checkboxes[1].fire("change");
  await preview.fire("click");
  assert.deepEqual(chosen.entryIds, ["p", "a"]);
  assert.equal(publish.disabled, false);
  assert.equal(publish.hidden, false);
  assert.deepEqual(dialog.querySelector(".social-preview").querySelectorAll("strong").map(node => node.textContent), ["1 · 元の投稿", "2 · 1への返信", "3 · 2への返信"]);
  checkboxes[1].checked = false; await checkboxes[1].fire("change");
  assert.equal(publish.disabled, true);
  assert.equal(publish.hidden, true, "changing selection removes the publish action until preview");
  await preview.fire("click");
  const sending = publish.fire("click");
  assert.equal(publish.disabled, true);
  assert.equal(publish.textContent, "投稿しています…");
  await publish.fire("click");
  assert.equal(publishCalls, 1);
  finishPublish(); await sending;
  assert.equal(publish.textContent, "投稿済み");
  assert.equal(dialog.querySelectorAll("a").length, 1);
  assert.equal(dialog.querySelector("a").href, "https://bsky.app/profile/example/post/root");
  assert.equal(preview.disabled, true);
  await publish.fire("click");
  assert.equal(publishCalls, 1);
  assert.deepEqual(posted.entryIds, ["p"]);
  assert.equal(posted.digest, "digest");
  assert.equal("credentials" in posted, false);
  assert.equal(publish.disabled, true);
  assert.equal(dialog.querySelectorAll("input").some((input) => input.value === "private health"), false);
  // A new reply can still be selected after publishing the root.
  checkboxes[1].checked = true; await checkboxes[1].fire("change");
  assert.equal(preview.disabled, false);
  await openSocial("p", (id) => ({name:id}));
  assert.equal(document.body.children.length, 1);
  dialog.close();
  t.mock.method(api, "previewSocial", async () => ({digest:"same", posts:[{entryId:"p", text:"posted", publication:{state:"posted", url:"https://bsky.app/profile/example/post/a"}}, {entryId:"a", parent:"p", text:"reply", publication:{state:"posted", url:"https://bsky.app/profile/example/post/b"}}]}));
  await openSocial("p", (id) => ({name:id}));
  const reopened = document.body.children[0];
  await reopened.querySelectorAll("button").find(b => b.textContent === "プレビュー").fire("click");
  const already = reopened.querySelectorAll("button").find(b => b.textContent === "投稿済み");
  assert.equal(already.disabled, true);
  assert.equal(reopened.querySelectorAll("a").length, 1);
  await already.fire("click");
  assert.equal(publishCalls, 1);
  reopened.close();
  t.mock.method(api, "previewSocial", async (body) => {
    chosen = body;
    return { digest: "answer", posts: [{ entryId: "a", text: "answer only" }] };
  });
  t.mock.method(api, "publishSocial", async (body) => {
    posted = body;
    return { error: "", posts: [{ entryId: "a", publication: { url: "https://bsky.app/profile/example/post/answer" } }] };
  });
  await openSocial("p", id => ({ name: id }));
  const only = document.body.children[0];
  const choices = only.querySelector(".social-selection").querySelectorAll("input");
  choices[0].checked = false; await choices[0].fire("change");
  const onlyPreview = only.querySelectorAll("button").find(b => b.textContent === "プレビュー");
  assert.equal(onlyPreview.disabled, true, "empty selection cannot preview");
  choices[1].checked = true; await choices[1].fire("change");
  assert.equal(onlyPreview.disabled, false);
  await onlyPreview.fire("click");
  assert.deepEqual(chosen.entryIds, ["a"]);
  assert.equal(only.querySelector(".social-preview").querySelector("strong").textContent, "1 · 最初の投稿");
  await only.querySelectorAll("button").find(b => b.textContent === "選択した会話を投稿").fire("click");
  assert.deepEqual(posted.entryIds, ["a"]);
  assert.equal(only.querySelector("a").href, "https://bsky.app/profile/example/post/answer");
  assert.equal(choices[0].disabled, false);
  only.close();
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
