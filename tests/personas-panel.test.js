import test from "node:test";
import assert from "node:assert/strict";
import { setLanguage, getLanguage } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createPersonas, fold, matches, byName, readingOf } = await import("../frontend/src/personas.js");
const { createSchedules } = await import("../frontend/src/schedules.js");
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
const content = node => node.textContent + node.children.map(content).join("");
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
test("a person's card chooses daily or monthly, without explaining check-ins", async (t) => {
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
  assert.deepEqual(sent[0], ["sato", { every: "monthly" }]);
  assert.deepEqual(every.children.map(node => node.textContent), ["頻繁に振り返る", "ときどき振り返る"]);
  assert.ok(!walk(view.element).some(node => String(node.textContent).startsWith("毎月12日")));
});

test("person calendar schedules show their annual recurrence", async (t) => {
  setup(t);
  t.mock.method(api, "personas", async () => ({ personas: [{ ...rows[2], schedules: [{ id: "birthday", at: "2027-11-10T00:00:00Z", timeZone: "Asia/Tokyo", note: "誕生日（当日の通知）", personEvent: { annual: true } }] }], tools: [] }));
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  assert.ok(walk(view.element).some(node => node.className.includes("schedule") && content(node).includes("毎年") && content(node).includes("誕生日")));
});

test("a person's card shows the basic information and offers to edit it", async (t) => {
  setup(t);
  t.mock.method(api, "personas", async () => ({ personas: [
    { ...rows[2], basicInfo: "誕生日は11月10日。\n釣りが好き。" },
    { ...rows[3] },
  ], tools: [] }));
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  const boxes = walk(view.element).filter((node) => node.className === "basic-info");
  assert.equal(boxes.length, 2, "every person's card has it");
  const texts = boxes.map((box) => walk(box).find((node) => node.className === "basic-info-text").textContent);
  assert.deepEqual(texts, ["誕生日は11月10日。\n釣りが好き。", "まだ登録されていません。"]);
  assert.ok(boxes.every((box) => walk(box).some((node) => node.textContent === "基本情報を編集")), "and a way to edit it");
  // A subject has none: basic information is a person's.
  tabs(view.element)[1].listeners.click();
  assert.equal(walk(view.element).filter((node) => node.className === "basic-info").length, 0);
});

test("a memoryless persona keeps editing and planner preferences without memory or check-in controls", async t => {
  setup(t);
  t.mock.method(api, "personas", async () => ({ personas: [{ id: "support", name: "support", builtin: true, active: true, memoryEnabled: false, domain: "kakerattaのサポート", tools: ["kakeratta_help"] }], tools: [], mcpConfigured: true }));
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  const all = walk(view.element);
  for (const label of ["修正", "AIで修正", "外部エージェントを優先する"]) assert.ok(all.some(n => n.textContent === label), label);
  for (const label of ["覚えていること", "チェックインでは最低限のことだけ話す"]) assert.ok(!all.some(n => n.textContent === label), label);
});


test("the separate schedules panel combines owners by next time and filters without persona cards", async t => {
 setup(t);
 const scheduled = [
  { ...rows[0], schedules: [{ id: "s_later", at: "2099-11-12T00:00:00Z", note: "夕食", notify: false }, { id: "s_first", at: "2099-11-09T00:00:00Z", note: "朝食" }] },
  { ...rows[2], schedules: [{ id: "s_early", at: "2099-11-10T00:00:00Z", note: "誕生日" }] },
 ];
 t.mock.method(api, "personas", async () => ({ personas: scheduled, tools: [] }));
 const view = createSchedules();
 await view.load();
 assert.deepEqual(tabs(view.element), []);
 assert.deepEqual(cards(view.element), []);
 const chips = () => walk(view.element).filter(n => n.className === "chip ghost schedule");
 assert.ok(content(chips()[0]).includes("朝食"));
 assert.ok(content(chips()[1]).includes("誕生日"));
 assert.ok(content(chips()[2]).includes("夕食"));
 assert.deepEqual(chips().map(chip => chip.children[0].className), ["schedule-owner", "schedule-owner", "schedule-owner"]);
 assert.ok(!walk(view.element).some(n => n.className === "notification-schedules-heading"));
 assert.ok(walk(view.element).some(n => n.textContent === "通知OFF"));
 const search = walk(view.element).find(n => n.className === "persona-search");
 search.value = "夕食"; search.listeners.input();
 assert.equal(chips().length, 1);
 search.value = "さとう"; search.listeners.input();
 assert.equal(chips().length, 1);
 search.value = "missing"; search.listeners.input();
 assert.ok(walk(view.element).some(n => n.textContent === "一致する予定がありません"));
 view.updateRows(scheduled.map(row => ({ ...row, schedules: [] })));
 assert.equal(chips().length, 0);
});

test("a schedule edit saves only changed fields and keeps input after failure", async t => {
 setup(t);
 const toasts = new Element("div"); document.querySelector = () => toasts;
 const scheduled = [{ ...rows[0], schedules: [{ id: "s_edit", at: "2099-11-12T00:00:31Z", note: "夕食" }] }];
 t.mock.method(api, "personas", async () => ({ personas: scheduled, tools: [] }));
 const update = t.mock.method(api, "updateSchedule", async () => { throw new Error("retry me"); });
 let changed;
 const view = createSchedules({ onChange: rows => { changed = rows; } }); await view.load();
 document.body = new Element("body");
 walk(view.element).find(n => n.className === "schedule-edit-button").listeners.click();
 const form = walk(document.body).find(n => n.className === "schedule-edit-form");
 const checkbox = walk(form).find(n => n.type === "checkbox");
 checkbox.checked = false;
 await form.listeners.submit({ preventDefault() {} });
 assert.deepEqual(update.mock.calls[0].arguments, ["gourmet", "s_edit", { notify: false }]);
 assert.equal(checkbox.checked, false);
 assert.ok(walk(toasts).some(n => n.textContent === "retry me"));
 const input = walk(form).find(n => n.type === "datetime-local"); input.value = "2099-11-13T09:30";
 update.mock.mockImplementation(async () => ({ personas: [{ ...scheduled[0], schedules: [] }] }));
 await form.listeners.submit({ preventDefault() {} });
 assert.equal(update.mock.calls[1].arguments[2].at, new Date("2099-11-13T09:30").toISOString());
 assert.ok(!walk(document.body).some(n => n.className === "schedule-edit-form"));
 assert.deepEqual(changed[0].schedules, []);
 assert.ok(content(view.element).includes("予定はありません"));
});

test("the schedules panel reports load failures and can retry", async t => {
 setup(t);
 const request = t.mock.method(api, "personas", async () => { throw new Error("offline"); });
 const view = createSchedules();
 await view.load();
 const status = walk(view.element).find(n => n.role === "status");
 assert.equal(status.textContent, "offline");
 assert.equal(status.hidden, false);
 request.mock.mockImplementation(async () => ({ personas: [] }));
 await view.load();
 assert.equal(status.hidden, true);
 assert.ok(content(view.element).includes("予定はありません"));
});

test("a remembered old schedules tab falls back to a persona kind", async t => {
 setup(t);
 globalThis.localStorage = { getItem: key => key === "kakeratta.personas.tab" ? "schedules" : null };
 t.after(() => { delete globalThis.localStorage; });
 const view = createPersonas({}); await view.load();
 assert.equal(tabs(view.element).length, 2);
 assert.deepEqual(cards(view.element), ["sato", "aoki", "nagomi"]);
});


test("activation keeps existing cards, scroll position and focus, and restores state on failure", async t => {
  setup(t);
  document.querySelector = () => new Element("div");
  let current = rows.map(row => ({ ...row }));
  t.mock.method(api, "personas", async () => ({ personas: current }));
  const save = t.mock.method(api, "setActive", async active => {
    current = current.map(row => ({ ...row, active: active.includes(row.id) }));
    return { personas: current };
  });
  const view = createPersonas({});
  await view.load();
  const card = walk(view.element).find(node => node["data-id"] === "sato");
  const toggle = walk(card).find(node => node.role === "switch");
  const every = walk(card).find(node => node.className === "check-in-every");
  const search = walk(view.element).find(node => node.className === "persona-search");
  view.element.scrollTop = 450;
  document.activeElement = toggle;
  toggle.checked = false;
  const work = toggle.listeners.change({ target: toggle });
  assert.equal(toggle.disabled, true);
  await work;
  assert.equal(walk(view.element).find(node => node["data-id"] === "sato"), card);
  assert.equal(walk(view.element).find(node => node.className === "persona-search"), search);
  assert.equal(view.element.scrollTop, 450);
  assert.equal(document.activeElement, toggle);
  assert.equal(toggle.checked, false);
  assert.equal(toggle.disabled, false);
  assert.equal(every.disabled, true);
  assert.equal(card.className, "persona ");
  assert.equal(view.rows().find(row => row.id === "sato").active, false);
  save.mock.mockImplementation(async () => { throw Error("offline"); });
  toggle.checked = true;
  await toggle.listeners.change({ target: toggle });
  assert.equal(toggle.checked, false);
  assert.equal(toggle.disabled, false);
  assert.equal(every.disabled, true);
  assert.equal(walk(view.element).find(node => node["data-id"] === "sato"), card);
});
