import test from "node:test";
import assert from "node:assert/strict";
import { setLanguage } from "../frontend/src/i18n.js";
import { api } from "../frontend/src/api.js";
import { showEntryDialog } from "../frontend/src/entry-dialog.js";

class Element {
  children = []; listeners = {}; textContent = ""; className = ""; dataset = {}; style = {};
  constructor(tag) { this.tag = tag; this.classList = { add() {}, remove() {}, contains: () => false, toggle() {} }; }
  setAttribute(key, value) { this[key] = value; }
  getAttribute(key) { return this[key]; }
  removeAttribute(key) { delete this[key]; }
  addEventListener(name, fn) { (this.listeners[name] ??= []).push(fn); }
  append(...items) { for (const item of items) { if (typeof item === "object") item.parent = this; this.children.push(item); } }
  prepend(...items) { this.children.unshift(...items); }
  replaceChildren(...items) { this.children = []; this.append(...items); }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  focus() {}
  scrollIntoView() {}
  querySelectorAll() { return []; }
  querySelector() { return null; }
  all() { return [this, ...this.children.flatMap((child) => child.all?.() || [])]; }
}

// A post a fact or a record rests on opens over the memory view, with its
// conversation, and closes back to it.
test("a remembered post opens in a modal with its conversation", async (t) => {
  setLanguage("ja");
  globalThis.Node = Element;
  globalThis.document = { body: new Element("body"), activeElement: null, createElement: (tag) => new Element(tag), createTextNode: (text) => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const asked = [];
  t.mock.method(api, "thread", async (id) => {
    asked.push(id);
    return { entries: [
      { id: "p", kind: "post", root: "p", speaker: "user", text: "神保町の担々麺はごまが強すぎた", at: "2026-09-01T11:14:00Z" },
      { id: "r", kind: "remark", root: "p", replyTo: "p", speaker: "gourmet", text: "ごま強めだったね", at: "2026-09-01T11:15:00Z" },
    ] };
  });
  const dialog = showEntryDialog("p", { getPersona: () => ({ id: "gourmet", name: "tabe", icon: "🍜" }), getPersonas: () => [], getSettings: () => ({}) });
  assert.ok(document.body.children.includes(dialog.element), "the modal was not shown");
  await dialog.loading;
  assert.deepEqual(asked, ["p"]);
  const text = dialog.element.all().map((node) => node.textContent).join("\n");
  assert.match(text, /神保町の担々麺はごまが強すぎた/);
  assert.match(text, /元の投稿/);
  dialog.close();
  assert.ok(!document.body.children.includes(dialog.element), "the modal did not close");

  // A post that is gone says so instead of an empty conversation.
  t.mock.method(api, "thread", async () => ({ entries: [] }));
  const gone = showEntryDialog("missing", {});
  await gone.loading;
  assert.match(gone.element.all().map((node) => node.textContent).join("\n"), /見つかりません/);
  gone.close();
});
