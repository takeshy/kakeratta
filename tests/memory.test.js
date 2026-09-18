import test from "node:test";
import assert from "node:assert/strict";
import { parseMemoryNote, memoryLink, createMemoryView, editableBody } from "../frontend/src/memory.js";
import { setLanguage } from "../frontend/src/i18n.js";

const preference = { path: "preferences/light.md", text: `---
type: Preference # free-form type
title: "Prefers lighter red wines"
description: >-
  Likes Pinot Noir
  and Gamay.
---
# Evidence
- [Sep 12 — liked Pinot Noir at restaurant](/?entry=post-12)
- [Sep 13 — described Gamay as easy to drink](/?entry=post-13)
# Exceptions
- Rich reds with winter meals.
# Wines
- [Domaine X Pinot Noir](/wines/pinot.md)
` };
const wine = { path: "wines/pinot.md", text: "---\ntype: Wine\ntitle: Domaine X Pinot Noir\n---\nTried at a restaurant." };

test("OKF display reads quoted and folded metadata and preserves evidence and exceptions", () => {
  const note = parseMemoryNote(preference);
  assert.equal(note.type, "Preference");
  assert.equal(note.title, "Prefers lighter red wines");
  assert.equal(note.description, "Likes Pinot Noir and Gamay.");
  assert.match(note.body, /# Evidence/);
  assert.match(note.body, /# Exceptions/);
  assert.equal(note.text, preference.text);
  assert.equal(parseMemoryNote({ path: "old.md", text: "Unstructured memory" }).body, "Unstructured memory");
  assert.match(parseMemoryNote({ path: "broken.md", text: "---\ntitle: unfinished" }).body, /unfinished/);
});

test("source navigation preserves entry IDs; bundle links resolve without inventing sources", () => {
  const paths = new Set([preference.path, wine.path]);
  assert.deepEqual(memoryLink("/?entry=a%2Fb", preference.path, paths), { href: "/?entry=a%2Fb", entry: true });
  assert.deepEqual(memoryLink("/wines/pinot.md", preference.path, paths), { note: wine.path });
  assert.deepEqual(memoryLink("../wines/pinot", preference.path, paths), { note: wine.path });
  for (const link of ["/missing.md", "javascript:alert(1)", "java\nscript:alert(1)", "data:text/html,bad", "/%ZZ"]) {
    assert.equal(memoryLink(link, preference.path, paths), null, link);
  }
  assert.deepEqual(memoryLink("https://example.com/wine", preference.path, paths), { href: "https://example.com/wine" });
});

class Element {
  children = []; attributes = {}; events = {}; textContent = "";
  constructor(tag) { this.tagName = tag.toUpperCase(); }
  setAttribute(key, value) { this.attributes[key] = value; }
  addEventListener(key, callback) { this.events[key] = callback; }
  append(...nodes) { this.children.push(...nodes); }
  scrollIntoView() { this.scrolled = true; }
  focus() { this.focused = true; }
  all() { return [this, ...this.children.flatMap(child => child.all())]; }
}

test("reader shows wine preferences, evidence links and raw fallback without executing note HTML", (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => Object.assign(new Element("text"), { textContent: text }),
  };
  setLanguage("en");
  ctx.after(() => { delete globalThis.document; delete globalThis.Node; setLanguage("ja"); });
  const view = createMemoryView([wine, preference, { path: "odd.md", text: "<img src=x onerror=alert(1)>\n[bad](javascript:alert)" }]);
  const nodes = view.all();
  const texts = nodes.map(node => node.textContent).join("\n");
  assert.match(texts, /Preferences/);
  assert.match(texts, /Wines/);
  assert.match(texts, /Evidence/);
  assert.match(texts, /Exceptions/);
  assert.match(texts, /View original note/);
  assert.ok(nodes.some(node => node.textContent === preference.text));
  assert.equal(nodes.filter(node => node.tagName === "IMG").length, 0);
  const links = nodes.filter(node => node.tagName === "A");
  assert.deepEqual(links.map(node => node.attributes.href), ["/?entry=post-12", "/?entry=post-13"]);
  assert.ok(links.every(node => node.attributes.target === "_blank" && node.attributes.rel.includes("noopener")));
  const noteLink = nodes.find(node => node.tagName === "BUTTON" && node.textContent === "Domaine X Pinot Noir");
  noteLink.events.click();
  assert.ok(nodes.some(node => node.tagName === "ARTICLE" && node.focused && node.scrolled));
  let searched;
  const legacy = createMemoryView([wine], { onSearch: query => { searched = query; } });
  const search = legacy.all().find(node => node.tagName === "BUTTON" && node.textContent === "Find original post");
  search.events.click();
  assert.equal(searched, wine.text.match(/title: (.+)/)[1]);
  const automatic = createMemoryView([{ path: "auto.md", text: "# Remembered fact\n\n<!-- kakeratta:note-sources -->\n# 記憶を更新した投稿\n\n* [Sep 12 — source](/?entry=real-post)\n<!-- /kakeratta:note-sources -->" }]);
  const automaticText = automatic.all().filter(node => node.tagName !== "PRE").map(node => node.textContent).join("\n");
  assert.match(automaticText, /Posts behind this memory/);
  assert.doesNotMatch(automaticText, /<!--/);
  assert.equal(automatic.all().find(node => node.tagName === "A").attributes.href, "/?entry=real-post");
  assert.match(createMemoryView([]).all().map(node => node.textContent).join(""), /Nothing remembered yet/);
});

test("individual forget confirms the selected title, handles cancellation and permits retry after failure", async (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => Object.assign(new Element("text"), { textContent: text }),
  };
  setLanguage("en");
  ctx.after(() => { delete globalThis.document; delete globalThis.Node; delete globalThis.confirm; setLanguage("ja"); });
  let accept = false, fail = true, prompt, calls = [];
  globalThis.confirm = text => { prompt = text; return accept; };
  const view = createMemoryView([preference, wine], { onForget: async path => {
    calls.push(path);
    if (fail) throw new Error("Offline");
  } });
  const buttons = view.all().filter(node => node.tagName === "BUTTON" && node.textContent === "Forget this note");
  assert.equal(buttons.length,2);
  await buttons[1].events.click();
  assert.deepEqual(calls,[]);
  assert.match(prompt,/Domaine X Pinot Noir/);
  accept = true;
  await buttons[1].events.click();
  assert.deepEqual(calls,[wine.path]);
  assert.ok(view.all().some(node => node.attributes.role === "alert" && node.textContent === "Offline" && !node.hidden));
  assert.equal(buttons[1].disabled,false);
  fail = false;
  await buttons[1].events.click();
  assert.deepEqual(calls,[wine.path,wine.path]);
  assert.ok(view.all().some(node => node.textContent === preference.title || node.textContent === "Prefers lighter red wines"));
});


// A note can be put right by hand: the editor holds the persona's text without
// the block the app appends, and saving hands back what was typed.
test("a note can be edited without touching the app's source block", async (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => { const node = new Element("#text"); node.textContent = text; return node; },
    activeElement: null,
  };
  ctx.after(() => { delete globalThis.Node; delete globalThis.document; });
  setLanguage("ja");
  const cited = { path: "wines/altra.md", text: "---\ntype: Wine\ntitle: ALTRA White Blend\n---\nノートンの白。\n\n<!-- kakeratta:note-sources -->\n# 記憶を更新した投稿\n\n* [ラベル](/?entry=p1)\n<!-- /kakeratta:note-sources -->\n" };
  assert.equal(editableBody(cited.text), "---\ntype: Wine\ntitle: ALTRA White Blend\n---\nノートンの白。");
  const edits = [];
  Element.prototype.replaceChildren = function (...nodes) { this.children = nodes; };
  const view = createMemoryView([cited], { onEdit: async (path, text) => { edits.push([path, text]); } });
  const button = view.all().find(node => node.tagName === "BUTTON" && node.textContent === "編集");
  assert.ok(button, "the note offers editing");
  button.events.click();
  const input = view.all().find(node => node.tagName === "TEXTAREA");
  assert.equal(input.value, editableBody(cited.text), "the editor shows the persona's text only");
  input.value = "---\ntype: Wine\ntitle: ALTURA White Blend\n---\nノートンの白。";
  const save = view.all().find(node => node.tagName === "BUTTON" && node.textContent === "メモを保存");
  await save.events.click();
  assert.deepEqual(edits, [["wines/altra.md", "---\ntype: Wine\ntitle: ALTURA White Blend\n---\nノートンの白。"]]);
  assert.equal(createMemoryView([cited], {}).all().some(node => node.tagName === "BUTTON" && node.textContent === "編集"), false, "without a handler there is nothing to edit with");
});
