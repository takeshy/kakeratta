import test from "node:test";
import assert from "node:assert/strict";
import { t as translate, getLanguage } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { createSpeaker } = await import("../frontend/src/speaker.js");
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
const hitBy = (id, speaker) => ({ entry: { id, kind: "remark", root: `r${id}`, speaker, at: "2026-09-01T00:00:00Z" }, snippet: `発言${id}` });
// The page comes with the conversations its remarks belong to.
const pageOf = (hits, total) => ({ hits, total, entries: hits.flatMap(({ entry }) => [
 { id: entry.root, kind: "post", root: entry.root, speaker: "user", at: "2026-08-31T00:00:00Z", text: entry.root }, entry]) });
function setup(t, options = {}) {
 globalThis.Node = TestNode;
 globalThis.document = { createElement: tag => new TestNode(tag), createTextNode: text => Object.assign(new TestNode("#text"), { textContent: text }) };
 t.after(() => { delete globalThis.Node; delete globalThis.document; });
 const view = createSpeaker({ getSettings: () => ({}), getPersona: () => null, ...options });
 return { view, status: nodes(view.element).find(n => n.role === "status"), more: nodes(view.element).find(n => n.textContent === translate("さらに読み込む")) };
}
test("a speaker's list shows what they said a page at a time, each under its post", async t => {
 const { view, status, more } = setup(t); const calls = [];
 const threads = t.mock.method(api, "thread", async () => { throw new Error("one request per page"); });
 t.mock.method(api, "speaker", async (id, opts) => { calls.push([id, opts]); return opts.offset === 0 ? pageOf([hitBy("a", "father"), hitBy("b", "father")], 3) : pageOf([hitBy("c", "father")], 3); });

 await view.show("father", "父");
 assert.deepEqual(calls[0], ["father", { limit: 20, offset: 0 }]);
 assert.equal(threads.mock.callCount(), 0, "no request per conversation");
 assert.ok(status.textContent.includes(translate("{0}件中{1}件を表示", 3, 2)));
 assert.equal(more.hidden, false, "a third is still to come");

 await more.listeners.click();
 assert.deepEqual(calls[1], ["father", { limit: 20, offset: 2 }]);
 assert.equal(more.hidden, true);

 // Another name replaces it; the user is named あなた.
 await view.show("user", "");
 assert.deepEqual(calls[2], ["user", { limit: 20, offset: 0 }]);
});
test("a page that arrives after another speaker was opened is dropped", async t => {
 const { view, more } = setup(t); let finish;
 t.mock.method(api, "speaker", async id => id === "father" ? await new Promise(resolve => { finish = resolve; }) : pageOf([hitBy("z", "mother")], 1));
 const opening = view.show("father", "父");
 await view.show("mother", "母");
 finish(pageOf([hitBy("a", "father")], 5));
 await opening;
 assert.equal(more.hidden, true, "the late page did not bring back its own \"more\"");
});
test("over the list is the persona as its card shows it, with the same ways to open it", async t => {
 const father = { id: "father", name: "父", icon: "👴", kind: "person", domain: "父。", aliases: ["お父さん"], keywords: ["釣り"], basicInfo: "11月10日生まれ。",
  active: true, planner: "external", wakes: 7, dailyCheckIn: { every: "monthly", on: 10, talk: "minimal" },
  tools: ["weather_forecast", "weather.weather_forecast", "web_search"],
  schedules: [
   { id: "birthday", at: "2026-11-10T00:00:00Z", timeZone: "Asia/Tokyo", note: "父の誕生日", personEvent: { annual: true } },
   { id: "daily", at: "2026-11-10T00:00:00Z", timeZone: "Asia/Tokyo", note: "毎朝の連絡", daily: true },
   { id: "once", at: "2026-11-12T00:00:00Z", timeZone: "Asia/Tokyo", note: "釣りの約束" },
  ] };
 const opened = [];
 const { view } = setup(t, { getPersona: id => id === "father" ? father : null,
  onOpenPersona: row => opened.push(["persona", row.id]), onOpenMemory: row => opened.push(["memory", row.id]), onEditPersona: (row, focus) => opened.push(["edit", row.id, focus]) });
 t.mock.method(api, "speaker", async () => pageOf([hitBy("a", "father")], 1));
 const host = nodes(view.element).find(n => n.className === "speaker-card-host");
 assert.equal(host.hidden, true, "no card before a name is opened");

 await view.show("father", "父");
 assert.equal(host.hidden, false);
 const card = nodes(host).find(n => n.className.includes("speaker-card"));
 const text = nodes(card).map(n => n.textContent).join("|");
 for (const want of ["父", "父。", translate("呼び名: {0}", "父 / お父さん"), translate("キーワード: {0}", "釣り"), "11月10日生まれ。"]) assert.ok(text.includes(want), `the card shows ${want}`);
 for (const want of [translate("有効"), translate("外部エージェント"), translate("起きた回数 {0}", 7),
  translate("この担当の通知予定"), "父の誕生日", "毎朝の連絡", "釣りの約束",
  translate("毎月{0}日、本体が月のまとめを書いたあとに起き、まとめと名前が出た投稿を読みます。話すのは、記録を忘れてよいかの確認など、あなたの判断が必要なときだけです。", 10)]) assert.ok(text.includes(want), `the card shows ${want}`);
 const schedules = nodes(card).filter(n => n.className === "chip ghost schedule");
 assert.equal(schedules.length, 3);
 assert.ok(schedules[0].textContent.startsWith(translate("毎年")));
 assert.ok(schedules[1].textContent.startsWith(translate("毎日")));
 const locale = getLanguage() === "ja" ? "ja-JP" : "en-US";
 const clock = new Intl.DateTimeFormat(locale, { timeZone: "Asia/Tokyo", hour: "2-digit", minute: "2-digit" }).format(new Date(father.schedules[0].at));
 assert.ok(schedules.every(n => n.textContent.includes(clock)), "schedule times use their saved time zone");
 const tools = nodes(card).filter(n => n.tag === "a");
 assert.equal(tools.length, 2, "qualified duplicates are folded into one tool");
 assert.equal(tools[0].textContent, translate("天気予報を調べる"));
 assert.equal(tools[0].href, `/manual/?lang=${getLanguage()}#${getLanguage()}-tool-weather_forecast`);
 const button = label => nodes(card).find(n => n.textContent === translate(label));
 button("担当の設定").listeners.click();
 button("修正").listeners.click();
 button("覚えていること").listeners.click();
 button("基本情報を編集").listeners.click();
 assert.deepEqual(opened, [["persona", "father"], ["edit", "father", undefined], ["memory", "father"], ["edit", "father", "basicInfo"]]);

 // Changed meanwhile, the card shows it when redrawn.
 father.basicInfo = "11月11日生まれ。";
 father.schedules = [];
 father.active = false;
 father.wakes = 8;
 view.redrawCard();
 assert.ok(nodes(host).map(n => n.textContent).join("|").includes("11月11日生まれ。"));
 assert.equal(nodes(host).filter(n => n.className === "chip ghost schedule").length, 0, "removed schedules disappear on refresh");
 assert.ok(nodes(host).some(n => n.textContent === translate("停止中")));
 assert.ok(nodes(host).some(n => n.textContent === translate("起きた回数 {0}", 8)));

 // The user has no card.
 await view.show("user", "");
 assert.equal(host.hidden, true);
});

test("a subject's speaker card shows its daily check-in and localized domain", async t => {
 const persona = { id: "gourmet", name: "tabe", builtin: true, active: true, domain: "食事を考える", dailyCheckIn: { talk: "share" } };
 const { view } = setup(t, { getPersona: () => persona });
 t.mock.method(api, "speaker", async () => pageOf([], 0));
 await view.show(persona.id);
 const host = nodes(view.element).find(n => n.className === "speaker-card-host");
 const all = nodes(host);
 assert.ok(all.some(n => n.textContent === translate("毎日7時〜21時のどこかで起き、最近の記録と自分の記憶を見直して、伝える価値があることを見つけたときに話します。")));
 assert.ok(all.some(n => n.textContent === translate("起きた回数 {0}", 0)));
 const domain = all.find(n => n.className === "domain").textContent;
 assert.equal(domain, getLanguage() === "ja" ? persona.domain : "Food, drinks, restaurants and meal photos; menus, preferences and choosing what to eat.");
 assert.ok(!all.some(n => n.className === "chip ghost schedule"));
});
