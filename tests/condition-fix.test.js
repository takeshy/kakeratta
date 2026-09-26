import test from "node:test";
import assert from "node:assert/strict";
import { getLanguage, setLanguage, t as translate } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { showConditionFix } = await import("../frontend/src/condition-fix.js");
const { api } = await import("../frontend/src/api.js");

class TestNode {
  children = [];
  listeners = {};
  dataset = {};
  className = "";
  value = "";
  textContent = "";
  hidden = false;
  append(...nodes) {
    for (const node of nodes) {
      if (!(node instanceof TestNode)) throw new Error(`appended ${String(node)}`);
      node.remove();
      node.parent = this;
      this.children.push(node);
    }
  }
  remove() {
    if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1);
    this.parent = null;
  }
  get firstChild() { return this.children[0]; }
  setAttribute(name, value) {
    if (name === "disabled") this.disabled = true;
    else this[name] = value;
  }
  addEventListener(name, fn) { (this.listeners[name] ??= []).push(fn); }
  fire(name, event = {}) { for (const fn of this.listeners[name] || []) fn(event); }
  focus() { document.activeElement = this; }
  querySelectorAll(selector) {
    return this.children.flatMap((child) => [
      ...(child.className.split(" ").includes(selector.slice(1)) ? [child] : []),
      ...child.querySelectorAll(selector),
    ]);
  }
}

const walk = (node) => [node, ...node.children.flatMap(walk)];
const visible = (node) => walk(document.body).filter((n) => !walk(document.body).some((p) => p.hidden && walk(p).includes(n) && p !== n));
const button = (label) => visible(document.body).find((node) => node.textContent === translate(label) && node.listeners.click);
const one = (cls) => document.body.querySelectorAll(cls)[0];
const settle = async () => { for (let i = 0; i < 6; i++) await new Promise(setImmediate); };

function setup(t) {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.mock.method(globalThis, "setTimeout", () => 0);
  const previous = getLanguage();
  setLanguage("ja");
  t.after(() => setLanguage(previous));
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
}

const persona = { id: "move", name: "michi", icon: "🚃", domain: "移動や場所の話。", tone: "短く", instructions: "場所を覚える", tools: ["route"] };
const entry = { id: "p", kind: "post", speaker: "user", text: "駅前のラーメン屋まで歩いた" };

// The person says why; the AI writes the condition; the person can correct it
// by hand or send the AI a new request; OK saves what is in the box and has
// the router try the post, and a landing as meant ends it.
test("a condition is written from the reason, corrected by hand or by the AI, saved, and checked", async (t) => {
  setup(t);
  const drafts = ["飲食店を含む、移動や場所の話。", "飲食店への行き方を含む、移動や場所の話。"];
  t.mock.method(api, "buildPersona", async () => ({ message: "直しました", draft: { ...persona, domain: drafts.shift() } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  t.mock.method(api, "checkRouting", async () => ({ answered: true, delivered: true, token: "route-1" }));
  t.mock.method(api, "applyRouting", async () => ({ applied: true }));
  let reloaded = 0;
  showConditionFix({ persona, entry, direction: "include", reason: "移動の話ではない", onSaved: () => { reloaded++; } });

  assert.ok(walk(document.body).some((n) => n.textContent === "駅前のラーメン屋まで歩いた"), "the post is quoted");
  assert.ok(walk(document.body).some((n) => n.textContent === translate("担当の判断：{0}", "移動の話ではない")), "the router's reason is shown");
  one(".condition-note").value = "店に行く話なので";
  button("条件を作る").fire("click");
  await settle();
  const first = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(first.id, "move");
  assert.equal(first.entryId, entry.id);
  assert.equal(first.current.domain, "移動や場所の話。");
  assert.equal(first.messages.length, 1);
  assert.match(first.messages[0].content, /駅前のラーメン屋まで歩いた/);
  assert.match(first.messages[0].content, /移動の話ではない/);
  assert.match(first.messages[0].content, /店に行く話なので/);
  assert.match(first.messages[0].content, /domain/);

  const next = one(".condition-new");
  assert.equal(next.value, "飲食店を含む、移動や場所の話。", "the AI's condition fills the editable box");
  assert.ok(one(".condition-ask").hidden, "the request to the AI is out of the way until asked for");

  // By hand first, then a new request to the AI, which works from the box.
  next.value = "飲食店を含む、移動や場所の話。写真も対象。";
  next.fire("input");
  button("AIに変更を依頼").fire("click");
  assert.ok(!one(".condition-ask").hidden);
  one(".condition-request").value = "行き方の話に限って";
  button("条件を作る").fire("click");
  await settle();
  const second = api.buildPersona.mock.calls[1].arguments[0];
  assert.equal(second.current.domain, "飲食店を含む、移動や場所の話。写真も対象。", "the AI starts from what is in the box");
  assert.equal(second.entryId, entry.id, "follow-up requests also carry the source post");
  assert.equal(second.messages.length, 3);
  assert.equal(second.messages[1].role, "assistant");
  assert.equal(second.messages[2].content, "行き方の話に限って");
  assert.equal(next.value, "飲食店への行き方を含む、移動や場所の話。", "the reply replaces the box");
  assert.ok(one(".condition-ask").hidden);

  next.value = "飲食店への行き方を含む、移動や場所の話。手直し。";
  next.fire("input");
  button("OK").fire("click");
  await settle();
  const saved = api.savePersonaDraft.mock.calls[0].arguments[0];
  assert.equal(saved.domain, "飲食店への行き方を含む、移動や場所の話。手直し。", "what is in the box is what is saved");
  assert.equal(saved.tone, "短く");
  assert.equal(saved.instructions, "場所を覚える");
  assert.deepEqual(saved.tools, ["route"]);
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[1], "move");
  assert.equal(reloaded, 1);
  assert.deepEqual(api.checkRouting.mock.calls[0].arguments, ["p", "move"]);
  assert.deepEqual(api.applyRouting.mock.calls[0].arguments, ["p", "route-1"]);
  assert.equal(document.body.children.length, 0, "landing as meant closes the dialog");
});

// The post still not going where it was meant to is said, with the router's
// reason, and the person is asked again; what was saved stays saved and the
// next request tells the AI about the failure.
test("a saved condition that still routes wrongly is reported and asked about again", async (t) => {
  setup(t);
  t.mock.method(api, "buildPersona", async (body) => ({ message: "", draft: { ...body.current, domain: body.current.domain + " 追加。" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  const checks = [{ answered: true, delivered: true }, { answered: false, error: "routing is not configured" }, { answered: true, delivered: false, token: "route-2" }];
  t.mock.method(api, "checkRouting", async () => checks.shift());
  t.mock.method(api, "applyRouting", async () => ({ applied: true }));
  showConditionFix({ persona, entry, direction: "exclude", onSaved: () => {} });

  one(".condition-note").value = "名前が出ただけ";
  button("条件を作る").fire("click");
  await settle();
  assert.match(api.buildPersona.mock.calls[0].arguments[0].messages[0].content, /対象にしないでほしい/);
  button("OK").fire("click");
  await settle();
  assert.equal(api.savePersonaDraft.mock.calls.length, 1);
  assert.ok(one(".condition-note"), "still reaching the persona sends the person back to the reason");
  const notice = one(".condition-notice");
  assert.equal(notice.textContent, translate("保存した条件でも、この投稿は {0} に届きました。", "michi"));
  assert.ok(walk(document.body).some((n) => n.textContent === "移動や場所の話。 追加。"), "the current condition is the one just saved");

  one(".condition-note").value = "もう一度";
  button("条件を作る").fire("click");
  await settle();
  const again = api.buildPersona.mock.calls[1].arguments[0];
  assert.equal(again.current.domain, "移動や場所の話。 追加。", "the AI works from the saved condition");
  assert.match(again.messages.at(-1).content, /届きました/, "the AI is told the last condition did not do it");
  assert.equal(again.messages.length, 3);

  // A router that cannot answer leaves the proposal up, saved, with a note.
  button("OK").fire("click");
  await settle();
  assert.equal(api.savePersonaDraft.mock.calls.length, 2);
  assert.ok(one(".condition-new"), "the proposal stays up");
  assert.match(walk(document.body).map((n) => n.textContent).join("\n"), /振り分けを確認できませんでした/);

  // Pressing OK again with the same text checks without saving again, and a
  // landing as meant closes.
  button("OK").fire("click");
  await settle();
  assert.equal(api.savePersonaDraft.mock.calls.length, 2, "an unchanged condition is not saved twice");
  assert.equal(api.applyRouting.mock.calls.length, 0, "excluding a past recipient does not erase its existing turn");
  assert.equal(document.body.children.length, 0);
});

test("cancel saves nothing and an unchanged proposal is not offered", async (t) => {
  setup(t);
  t.mock.method(api, "buildPersona", async (body) => ({ message: "そのままです", draft: { ...body.current } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  showConditionFix({ persona, entry, direction: "include", reason: "x" });
  one(".condition-note").value = "y";
  button("条件を作る").fire("click");
  await settle();
  assert.ok(one(".condition-note"), "an unchanged condition keeps the person on the reason");
  assert.equal(document.body.querySelectorAll(".condition-new").length, 0);
  assert.match(walk(document.body).map((n) => n.textContent).join("\n"), /条件を変えられませんでした/);
  button("キャンセル").fire("click");
  assert.equal(api.savePersonaDraft.mock.calls.length, 0);
  assert.equal(document.body.children.length, 0);
});

// A person has no condition: a post that should have reached one adds
// keywords, from the post and near it, and the check is whether the post now
// has one of them.
test("a person's missed post adds keywords from the post rather than a condition", async (t) => {
  setup(t);
  const sato = { id: "sato", name: "佐藤さん", icon: "🧑", domain: "佐藤さんのこと。", tools: [], kind: "person", aliases: ["サトウ"], keywords: ["キャンプ"] };
  const post = { id: "q", kind: "post", speaker: "user", text: "週末は海釣りに行くらしい" };
  t.mock.method(api, "buildPersona", async ({ current }) => ({ message: "足しました", draft: { ...current, keywords: [...current.keywords, "海釣り", "釣り"] } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  const checks = [{ answered: true, delivered: false, reason: "呼び名・キーワードが投稿にありませんでした" }, { answered: true, delivered: true, heard: "海釣り", token: "route-2" }];
  t.mock.method(api, "checkRouting", async () => checks.shift());
  t.mock.method(api, "applyRouting", async () => ({ applied: true }));
  showConditionFix({ persona: sato, entry: post, direction: "include", reason: "呼び名・キーワードが投稿にありませんでした" });

  assert.ok(walk(document.body).some((n) => n.textContent === "キャンプ"), "the current keywords are shown");
  button("キーワードを作る").fire("click");
  await settle();
  const asked = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(asked.entryId, "q");
  assert.match(asked.messages[0].content, /週末は海釣りに行くらしい/);
  assert.match(asked.messages[0].content, /keywords/);
  const next = one(".condition-new");
  assert.equal(next.value, "海釣り、釣り", "only what is added is proposed");

  // A proposal the post does not contain is said, and asked again.
  next.value = "渓流";
  next.fire("input");
  button("OK").fire("click");
  await settle();
  assert.deepEqual(api.savePersonaDraft.mock.calls[0].arguments[0].keywords, ["キャンプ", "渓流"]);
  assert.equal(api.applyRouting.mock.calls.length, 0);
  assert.ok(walk(document.body).some((n) => n.textContent === translate("足したキーワードはどれもこの投稿に書かれていないので、{0} に届きませんでした。", "佐藤さん")));

  button("キーワードを作る").fire("click");
  await settle();
  assert.match(api.buildPersona.mock.calls[1].arguments[0].messages.at(-1).content, /届きませんでした/, "the failure is told to the AI");
  button("OK").fire("click");
  await settle();
  const saved = api.savePersonaDraft.mock.calls[1].arguments[0];
  assert.deepEqual(saved.keywords, ["キャンプ", "渓流", "海釣り", "釣り"]);
  assert.equal(saved.kind, "person");
  assert.deepEqual(saved.aliases, ["サトウ"]);
  assert.deepEqual(api.applyRouting.mock.calls[0].arguments, ["q", "route-2"]);
  assert.equal(document.body.children.length, 0);
});
