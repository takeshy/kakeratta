import test from "node:test";
import assert from "node:assert/strict";
import { setLanguage, getLanguage } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createPersonas, fold, matches, byName, readingOf } = await import("../frontend/src/personas.js");
const { api } = await import("../frontend/src/api.js");

class Element {
  children = []; listeners = {}; textContent = ""; className = ""; value = "";
  classList = { add() {}, remove() {} };
  constructor(tag) { this.tag = tag; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  get firstChild() { return this.children[0]; }
  setAttribute(key, value) { this[key] = key === "disabled" || key === "checked" ? true : value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
  scrollIntoView() { this.scrolled = true; }
  querySelector(selector) {
    const id = selector.match(/data-id="([^"]+)"/)?.[1];
    return walk(this).find((node) => node !== this && node["data-id"] === id) || null;
  }
}
const walk = (node) => [node, ...node.children.flatMap(walk)];
const cards = (root) => walk(root).filter((node) => node.tag === "section" && node["data-id"]).map((node) => node["data-id"]);
const tabs = (root) => walk(root).filter((node) => node.role === "tab");

const rows = [
  { id: "gourmet", name: "tabe", icon: "🍜", builtin: true, active: true, tools: [], domain: "食べもの・飲みもの・飲食店の話や写真。" },
  { id: "wine", name: "budou", icon: "🍷", active: true, tools: [], domain: "ワインの話。" },
  { id: "sato", name: "佐藤さん", icon: "🧑", active: true, tools: ["schedule"], kind: "person", aliases: ["佐藤", "サトウ", "健二"], keywords: ["キャンプ", "釣り"], domain: "佐藤さんのこと。" },
  { id: "aoki", name: "青木", icon: "👩", active: true, tools: ["schedule"], kind: "person", aliases: ["あおき"], keywords: ["ピアノ"], domain: "青木さんのこと。" },
  { id: "nagomi", name: "nagomi", icon: "🌸", active: true, tools: ["schedule"], kind: "person", aliases: ["なごみ", "恋人"], keywords: [], domain: "nagomiのこと。" },
];

function setup(t) {
  globalThis.Node = Element;
  globalThis.document = { createElement: (tag) => new Element(tag), createTextNode: (text) => Object.assign(new Element("text"), { textContent: text }) };
  t.mock.method(globalThis, "setTimeout", () => 0);
  t.mock.method(api, "personas", async () => ({ personas: rows, tools: [] }));
  const previous = getLanguage();
  setLanguage("ja");
  t.after(() => { setLanguage(previous); delete globalThis.Node; delete globalThis.document; });
}

test("names are folded and people sort by the reading in their names", () => {
  assert.equal(fold("サトウ Kenji"), "さとうkenji");
  assert.equal(fold("ＡＢＣ"), "abc");
  assert.ok(matches(rows[2], "さとう"), "a name in katakana is found in hiragana");
  assert.ok(matches(rows[2], "釣り"), "a keyword finds a person");
  assert.ok(matches(rows[0], "飲食店"), "a subject is found by what it covers");
  assert.ok(!matches(rows[2], "ワイン"));
  assert.ok(matches(rows[2], "佐藤 キャンプ"), "every word must be there");
  assert.ok(!matches(rows[2], "佐藤 ワイン"));
  assert.equal(readingOf(rows[2]), "サトウ");
  assert.equal(readingOf(rows[4]), "nagomi");
  assert.deepEqual(byName(rows.slice(2), "ja").map((row) => row.id), ["nagomi", "aoki", "sato"]);
});

test("people and interests are on tabs of their own, each searchable, people in name order", async (t) => {
  setup(t);
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  assert.deepEqual(tabs(view.element).map((tab) => tab.textContent), ["人物 3", "関心ごと 2"]);
  assert.deepEqual(cards(view.element), ["sato", "aoki", "nagomi"], "people come first, as added");
  const order = walk(view.element).find((node) => node.className === "persona-order");
  order.value = "name";
  order.listeners.change();
  assert.deepEqual(cards(view.element), ["nagomi", "aoki", "sato"], "青木 is read あおき, 佐藤さん サトウ");

  const search = walk(view.element).find((node) => node.className === "persona-search");
  search.value = "さとう";
  search.listeners.input();
  assert.deepEqual(cards(view.element), ["sato"]);
  search.value = "ワイン";
  search.listeners.input();
  assert.deepEqual(cards(view.element), []);
  assert.ok(walk(view.element).some((node) => node.textContent === "一致する担当がありません"));

  // A persona asked for from elsewhere is shown even when it is on the other
  // tab or searched away.
  view.reveal("wine");
  assert.deepEqual(cards(view.element), ["gourmet", "wine"]);
  assert.equal(tabs(view.element)[1]["aria-selected"], "true");
  assert.equal(walk(view.element).find((node) => node.className === "persona-search").value, "");
});

// A person can be looked back on monthly; a subject is not offered it.
test("a person's card chooses daily or monthly, and says the day", async (t) => {
  setup(t);
  const sent = [];
  let current = rows.map((row) => ({ ...row }));
  t.mock.method(api, "personas", async () => ({ personas: current, tools: [] }));
  t.mock.method(api, "setDailyCheckIn", async (id, body) => {
    sent.push([id, body]);
    current = current.map((row) => row.id === id ? { ...row, dailyCheckIn: { talk: body.talk, every: body.every, on: body.every ? 12 : 0 } } : row);
    return { personas: current };
  });
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  tabs(view.element)[1].listeners.click();
  assert.equal(walk(view.element).filter((node) => node.className === "check-in-every").length, 0, "no choice on interests");
  tabs(view.element)[0].listeners.click();
  const every = walk(view.element).filter((node) => node.className === "check-in-every")[0];
  every.value = "monthly";
  await every.listeners.change();
  for (let i = 0; i < 4; i++) await new Promise(setImmediate);
  assert.deepEqual(sent[0], ["sato", { talk: "share", every: "monthly" }]);
  assert.ok(walk(view.element).some((node) => String(node.textContent).startsWith("毎月12日")), "the card says the day");
});
