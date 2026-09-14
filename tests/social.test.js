import test from "node:test";
import assert from "node:assert/strict";
import { setLanguage } from "../frontend/src/i18n.js";
import { api } from "../frontend/src/api.js";
import { openSocial } from "../frontend/src/social.js";

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
  let chosen, posted;
  t.mock.method(api, "previewSocial", async (body) => {
    chosen = body;
    return { digest: "digest", posts: body.entryIds.flatMap((id) => id === "p" ? [{ entryId: id, text: id }, { entryId: id, id: "p:part:2", text: "つづき:\ncontinued", parent: "p" }] : [{ entryId: id, text: id, parent: "p:part:2" }]) };
  });
  t.mock.method(api, "publishSocial", async (body) => { posted = body; return { error: "", posts: [] }; });
  await openSocial("p", (id) => ({ name: id, icon: "🍚" }));
  const dialog = document.body.children[0];
  const checkboxes = dialog.querySelector(".social-selection").querySelectorAll("input");
  assert.deepEqual(checkboxes.map((c) => c.checked), [true, false, false]);
  const buttons = dialog.querySelectorAll("button");
  const preview = buttons.find((b) => b.textContent === "プレビュー");
  const publish = buttons.find((b) => b.textContent === "選択した会話を投稿");
  assert.equal(publish.disabled, true);
  checkboxes[1].checked = true; await checkboxes[1].fire("change");
  await preview.fire("click");
  assert.deepEqual(chosen.entryIds, ["p", "a"]);
  assert.equal(publish.disabled, false);
  assert.deepEqual(dialog.querySelector(".social-preview").querySelectorAll("strong").map(node => node.textContent), ["1 · 元の投稿", "2 · 1への返信", "3 · 2への返信"]);
  checkboxes[1].checked = false; await checkboxes[1].fire("change");
  assert.equal(publish.disabled, true);
  await preview.fire("click");
  await publish.fire("click");
  assert.deepEqual(posted.entryIds, ["p"]);
  assert.equal(posted.digest, "digest");
  assert.equal("credentials" in posted, false);
  assert.equal(publish.disabled, true);
  assert.equal(dialog.querySelectorAll("input").some((input) => input.value === "private health"), false);
});
