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
  const glued = parseMemoryNote({ path: "glued.md", text: "---\ntype: Wine\ntitle: Altura\ntimestamp: 2026-09-18T21:06:00+09:00---\n\n基本情報\n生産者: Norton" });
  assert.equal(glued.type, "Wine");
  assert.equal(glued.title, "Altura");
  assert.match(glued.body, /^基本情報/);
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
  replaceChildren(...nodes) { this.children = nodes; }
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
  const view = createMemoryView([cited], { onEdit: async (path, text) => { edits.push([path, text]); } });
  const button = view.all().find(node => node.tagName === "BUTTON" && node.textContent === "✎");
  assert.ok(button, "the note carries a pencil");
  button.events.click();
  const input = view.all().find(node => node.tagName === "TEXTAREA");
  assert.equal(input.value, editableBody(cited.text), "the editor shows the persona's text only");
  input.value = "---\ntype: Wine\ntitle: ALTURA White Blend\n---\nノートンの白。";
  const save = view.all().find(node => node.tagName === "BUTTON" && node.textContent === "メモを保存");
  await save.events.click();
  assert.deepEqual(edits, [["wines/altra.md", "---\ntype: Wine\ntitle: ALTURA White Blend\n---\nノートンの白。"]]);
  assert.equal(createMemoryView([cited], {}).all().some(node => node.tagName === "BUTTON" && node.textContent === "✎"), false, "without a handler there is no pencil");
});


// Tags come out of the front matter as chips, and one chip narrows the view
// to the notes that carry it.
test("tags are shown as chips and narrow the view", (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => Object.assign(new Element("text"), { textContent: text }),
    activeElement: null,
  };
  ctx.after(() => { delete globalThis.Node; delete globalThis.document; });
  setLanguage("ja");
  const altura = { path: "wines/altura.md", text: "---\ntype: Wine\ntitle: Altura\ntags: [白ワイン, \"アルゼンチン\"]\n---\n白。" };
  const margaux = { path: "wines/margaux.md", text: "---\ntype: Wine\ntitle: Margaux\ntags:\n  - 赤ワイン\n  - ボルドー\n---\n赤。" };
  assert.deepEqual(parseMemoryNote(altura).tags, ["白ワイン", "アルゼンチン"]);
  assert.deepEqual(parseMemoryNote(margaux).tags, ["赤ワイン", "ボルドー"]);
  assert.deepEqual(parseMemoryNote(wine).tags, []);
  const view = createMemoryView([altura, margaux]);
  const chips = () => view.all().filter(node => node.tagName === "BUTTON" && String(node.textContent).startsWith("#"));
  assert.deepEqual(chips().map(node => node.textContent), ["#白ワイン", "#アルゼンチン", "#赤ワイン", "#ボルドー"]);
  chips()[3].events.click();
  const titles = () => view.all().filter(node => node.tagName === "H4").map(node => node.textContent);
  assert.deepEqual(titles(), ["Margaux"], "only the tagged note remains");
  assert.ok(view.all().some(node => node.textContent === "タグ「ボルドー」のメモ"));
  view.all().find(node => node.tagName === "BUTTON" && node.textContent === "すべてのメモを表示").events.click();
  assert.deepEqual(titles(), ["Altura", "Margaux"]);
});

// One page of many: a link to a note on a later page still resolves and asks
// for it, a tag asks the server rather than narrowing the page, and the next
// page is one button away.
test("reader pages notes and fetches what it has not read", async (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => Object.assign(new Element("text"), { textContent: text }),
  };
  ctx.after(() => { delete globalThis.document; delete globalThis.Node; });
  const asked = { open: [], filter: [], more: 0 };
  const view = createMemoryView([preference], {
    allPaths: [preference.path, wine.path, "wines/other.md"],
    onOpenMissing: path => asked.open.push(path),
    onFilter: filter => asked.filter.push(filter),
    onMore: async () => { asked.more++; },
  });
  const nodes = view.all();
  nodes.find(node => node.tagName === "BUTTON" && node.textContent === "Domaine X Pinot Noir").events.click();
  assert.deepEqual(asked.open, [wine.path]);
  assert.ok(nodes.some(node => node.textContent === "3件中1件を表示"));
  await nodes.find(node => node.tagName === "BUTTON" && node.textContent === "さらに読み込む").events.click();
  assert.equal(asked.more, 1);

  const tagged = { ...wine, text: "---\ntype: Wine\ntitle: Domaine X Pinot Noir\ntags: [赤]\n---\n" };
  const narrowed = createMemoryView([tagged], { filter: { tag: "赤" }, onFilter: filter => asked.filter.push(filter) });
  assert.ok(narrowed.all().some(node => node.textContent === "タグ「赤」のメモ"));
  narrowed.all().find(node => node.tagName === "BUTTON" && node.textContent === "すべてのメモを表示").events.click();
  assert.deepEqual(asked.filter, [{ tag: "", type: "" }]);
  assert.equal(narrowed.all().some(node => node.textContent === "さらに読み込む"), false, "no more button on the last page");
  // The type is a label on the card that asks for its own kind, keeping the tag.
  narrowed.all().find(node => node.tagName === "BUTTON" && node.textContent === "ワイン").events.click();
  assert.deepEqual(asked.filter.at(-1), { tag: "赤", type: "Wine" });
});

// The notes are one list in the order given, not sections by type: the type is
// a label on each card, and it narrows the list the way a tag does.
test("reader lists notes in order with the type as a label that narrows", (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => Object.assign(new Element("text"), { textContent: text }),
  };
  ctx.after(() => { delete globalThis.document; delete globalThis.Node; });
  setLanguage("ja");
  const shop = { path: "shops/a.md", text: "---\ntype: Shop\ntitle: A\n---\n" };
  const red = { path: "wines/red.md", text: "---\ntype: Wine\ntitle: Red\n---\n" };
  const taste = { path: "tastes/b.md", text: "---\ntype: Preference\ntitle: B\n---\n" };
  const view = createMemoryView([shop, red, taste]);
  const titles = () => view.all().filter(node => node.tagName === "H4").map(node => node.textContent);
  assert.deepEqual(titles(), ["A", "Red", "B"], "the order given, no grouping");
  assert.equal(view.all().some(node => node.tagName === "H3"), false, "no section headings");
  view.all().find(node => node.tagName === "BUTTON" && node.textContent === "ワイン").events.click();
  assert.deepEqual(titles(), ["Red"]);
  assert.ok(view.all().some(node => node.textContent === "種類「ワイン」のメモ"));
  view.all().find(node => node.tagName === "BUTTON" && node.textContent === "すべてのメモを表示").events.click();
  assert.deepEqual(titles(), ["A", "Red", "B"]);
});

test("memory overview appears closed before notes, with history after them", (ctx) => {
  globalThis.Node = Element;
  globalThis.document = {
    createElement: tag => new Element(tag),
    createTextNode: text => Object.assign(new Element("text"), { textContent: text }),
  };
  ctx.after(() => { delete globalThis.document; delete globalThis.Node; });
  const view = createMemoryView([
    wine,
    { path: "log.md", text: "# History" },
    { path: "index.md", text: "# Overview\n- [Wine](/wines/pinot.md)" },
  ]);
  assert.deepEqual(view.children.map(node => node.attributes["data-path"] || node.className),
    ["index.md", "memory-list", "log.md"]);
  assert.equal(Object.hasOwn(view.children[0].attributes, "open"), false);
  assert.equal(Object.hasOwn(view.children[2].attributes, "open"), false);
});
