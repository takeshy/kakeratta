import test from "node:test";
import assert from "node:assert/strict";
import { getLanguage, setLanguage, t as translate } from "../frontend/src/i18n.js";

globalThis.EventSource = class { close() {} };
const { showReplyFix } = await import("../frontend/src/reply-fix.js");
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
const button = (label) => walk(document.body).find((node) => node.textContent === translate(label) && node.listeners.click);
const one = (cls) => document.body.querySelectorAll(cls)[0];
const texts = () => walk(document.body).map((n) => n.textContent).join("\n");
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

const persona = { id: "gourmet", name: "tabe", icon: "🍜", domain: "食事。", tone: "丁寧に長く", instructions: "店を三つ勧める", model: "high", tools: ["place_search"] };
const post = { id: "p", kind: "post", speaker: "user", text: "この前の担々麺、ごまが強すぎた" };
const remark = { id: "r", kind: "remark", speaker: "gourmet", root: "p", replyTo: "p", text: "おすすめの店を三つご紹介します。一つ目は…" };

// The person says how the reply missed; the AI rewrites the tone and the
// instructions and nothing else; the person corrects both by hand; OK saves
// them and shows the reply the persona would now give on the same post.
test("a reply is disputed, the tone and instructions are rewritten, saved, and tried on the same post", async (t) => {
  setup(t);
  // The AI always changes the tone; a second round on the saved tone changes it again.
  t.mock.method(api, "buildPersona", async (body) => ({ message: "直しました", draft: { ...body.current, tone: body.current.tone === "短く一言" ? "一言だけ" : "短く一言", instructions: "前の店との違いを言う", domain: "変えてはいけない" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  t.mock.method(api, "previewReply", async () => ({ spoke: "ごまが前に出る店は前も苦手でしたね。", quiet: false }));
  let reloaded = 0;
  showReplyFix({ persona, remark, post, onSaved: () => { reloaded++; } });

  assert.match(texts(), /おすすめの店を三つ/, "the remark is quoted");
  assert.match(texts(), /担々麺/, "the post is shown");
  one(".reply-note").value = "店を並べないで、前との違いを一言で";
  button("直し方を作る").fire("click");
  await settle();
  const first = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(first.current.tone, "丁寧に長く");
  assert.match(first.messages[0].content, /担々麺/);
  assert.match(first.messages[0].content, /おすすめの店を三つ/);
  assert.match(first.messages[0].content, /前との違いを一言で/);
  assert.match(first.messages[0].content, /tone/);

  const tone = one(".reply-tone");
  const instructions = one(".reply-instructions");
  assert.equal(tone.value, "短く一言");
  assert.equal(instructions.value, "前の店との違いを言う");
  assert.ok(one(".condition-ask").hidden);

  // By hand, then a fresh request to the AI that works from the boxes.
  instructions.value = "前の店との違いを言う。店名は一つまで。";
  button("AIに変更を依頼").fire("click");
  one(".condition-request").value = "敬語はやめて";
  button("直し方を作る").fire("click");
  await settle();
  const second = api.buildPersona.mock.calls[1].arguments[0];
  assert.equal(second.current.instructions, "前の店との違いを言う。店名は一つまで。", "the AI starts from the boxes");
  assert.equal(second.messages.length, 3);
  assert.equal(second.messages[2].content, "敬語はやめて");

  button("OK").fire("click");
  await settle();
  const saved = api.savePersonaDraft.mock.calls[0].arguments[0];
  assert.equal(saved.tone, "一言だけ", "what is in the boxes is what is saved");
  assert.equal(saved.instructions, "前の店との違いを言う");
  assert.equal(saved.domain, "食事。", "the condition is not the AI's to change here");
  assert.deepEqual(saved.tools, ["place_search"]);
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[1], "gourmet");
  assert.equal(reloaded, 1);
  assert.deepEqual(api.previewReply.mock.calls[0].arguments, ["r", "gourmet"]);
  assert.match(texts(), /ごまが前に出る店は前も苦手でしたね/, "the trial reply is shown");

  // Still not it: back to saying how, with the trial as the remark in dispute.
  button("まだ違う").fire("click");
  assert.ok(one(".reply-note"));
  assert.match(texts(), /ごまが前に出る店は前も苦手でしたね/);
  one(".reply-note").value = "もっと短く";
  button("直し方を作る").fire("click");
  await settle();
  const third = api.buildPersona.mock.calls[2].arguments[0];
  assert.equal(third.current.tone, "一言だけ", "the AI works from what was saved");
  assert.match(third.messages.at(-1).content, /まだ期待と違った/);
  assert.match(third.messages.at(-1).content, /ごまが前に出る店/);
  assert.equal(third.messages.length, 5);

  button("OK").fire("click");
  await settle();
  button("これでよい").fire("click");
  assert.equal(document.body.children.length, 0, "accepting closes");
});

test("a quiet trial is said, and cancel saves nothing", async (t) => {
  setup(t);
  t.mock.method(api, "buildPersona", async (body) => ({ message: "", draft: { ...body.current, tone: "静かに" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  t.mock.method(api, "previewReply", async () => ({ spoke: "", quiet: true }));
  showReplyFix({ persona, remark, post });
  one(".reply-note").value = "x";
  button("直し方を作る").fire("click");
  await settle();
  button("OK").fire("click");
  await settle();
  assert.match(texts(), /何も言いませんでした/);
  button("キャンセル").fire("click");
  assert.equal(document.body.children.length, 0);

  showReplyFix({ persona, remark, post });
  one(".reply-note").value = "y";
  button("直し方を作る").fire("click");
  await settle();
  button("キャンセル").fire("click");
  assert.equal(api.savePersonaDraft.mock.calls.length, 1, "cancel after a proposal saves nothing more");
  assert.equal(document.body.children.length, 0);
});

test("full remarks and trials remain visible and reach every builder request", async (t) => {
  setup(t);
  const fullPost = "投稿".repeat(200) + "投稿の末尾";
  const fullRemark = "返事".repeat(200) + "問題の末尾";
  const fullTrial = "試し書き".repeat(100) + "試し書きの末尾";
  t.mock.method(api, "buildPersona", async body => ({ draft: { ...body.current, tone: body.current.tone + "短く" } }));
  t.mock.method(api, "savePersonaDraft", async skill => skill);
  t.mock.method(api, "previewReply", async () => ({ spoke: fullTrial, quiet: false }));
  showReplyFix({ persona, remark: { ...remark, text: fullRemark }, post: { ...post, text: fullPost } });
  assert.ok(texts().includes(fullRemark));
  button("直し方を作る").fire("click");
  await settle();
  const first = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(first.entryId, remark.id);
  assert.ok(first.messages[0].content.includes(fullRemark));
  assert.ok(first.messages[0].content.includes(fullPost));
  button("AIに変更を依頼").fire("click");
  one(".condition-request").value = "末尾も直して";
  button("直し方を作る").fire("click");
  await settle();
  assert.equal(api.buildPersona.mock.calls[1].arguments[0].entryId, remark.id);
  button("OK").fire("click");
  await settle();
  assert.ok(texts().includes(fullTrial), "the entire trial can be judged");
  button("まだ違う").fire("click");
  assert.ok(texts().includes(fullTrial));
  button("直し方を作る").fire("click");
  await settle();
  const retry = api.buildPersona.mock.calls[2].arguments[0];
  assert.equal(retry.entryId, remark.id);
  assert.ok(retry.messages.at(-1).content.includes(fullTrial));
});

test("save failures and preview failures are distinguished and retry preserves saved state", async (t) => {
  setup(t);
  t.mock.method(api, "buildPersona", async body => ({ draft: { ...body.current, tone: "短く" } }));
  let saveFails = true;
  t.mock.method(api, "savePersonaDraft", async skill => {
    if (saveFails) throw new Error("save failed");
    return skill;
  });
  let previewFails = true;
  t.mock.method(api, "previewReply", async () => {
    if (previewFails) throw new Error("preview failed");
    return { spoke: "返事", quiet: false };
  });
  showReplyFix({ persona, remark, post });
  button("直し方を作る").fire("click");
  await settle();
  button("OK").fire("click");
  await settle();
  assert.ok(texts().includes(translate("口調と指示を保存できませんでした")));
  assert.ok(!texts().includes(translate("保存しましたが、返事を試せませんでした")));
  assert.equal(api.previewReply.mock.calls.length, 0);
  assert.equal(one(".reply-tone").value, "短く");
  saveFails = false;
  button("OK").fire("click");
  await settle();
  assert.ok(texts().includes(translate("保存しましたが、返事を試せませんでした")));
  assert.equal(api.savePersonaDraft.mock.calls.length, 2);
  previewFails = false;
  button("OK").fire("click");
  await settle();
  assert.equal(api.savePersonaDraft.mock.calls.length, 2, "retrying the preview does not save again");
  assert.ok(button("これでよい"));
});

// A persona that was given a post and said nothing can be asked for a reply:
// the dispute is about its silence, and the trial runs from the post itself.
test("a quiet turn is disputed from the post and tried again from it", async (t) => {
  setup(t);
  t.mock.method(api, "buildPersona", async (body) => ({ message: "", draft: { ...body.current, instructions: "ラベルの写真には銘柄と品種を一言で返す" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  t.mock.method(api, "previewReply", async () => ({ spoke: "ノートンのアルトゥーラ、白のブレンドです。", quiet: false }));
  showReplyFix({ persona, remark: null, post: { id: "p2", kind: "post", speaker: "user", text: "ワインのラベル" } });
  assert.match(texts(), /何も言いませんでした/, "silence is what is being disputed");
  assert.match(texts(), /に返事してほしかった/);
  one(".reply-note").value = "銘柄を教えてほしかった";
  button("直し方を作る").fire("click");
  await settle();
  const first = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(first.entryId, "p2", "the post stands in for the missing remark");
  assert.match(first.messages[0].content, /何も言わなかった/);
  assert.match(first.messages[0].content, /銘柄を教えてほしかった/);
  button("OK").fire("click");
  await settle();
  assert.deepEqual(api.previewReply.mock.calls[0].arguments, ["p2", "gourmet"], "the trial runs from the post");
  assert.match(texts(), /ノートンのアルトゥーラ/);
});
