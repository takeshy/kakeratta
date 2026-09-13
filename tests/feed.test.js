import test from "node:test";
import assert from "node:assert/strict";
import { t as translate } from "../frontend/src/i18n.js";

// The feed now imports the shared dictation transport, which subscribes to SSE.
globalThis.EventSource = class { close() {} };
const { SILENCE_NOTE_UNTIL, showsSilenceNote, createFeed } = await import("../frontend/src/feed.js");
const { api } = await import("../frontend/src/api.js");

// Silence is the design. A timeline with nothing in it yet cannot show that,
// so the note is said while the timeline is new and stops once it is not.
test("the silence note is for a new timeline only", () => {
  assert.equal(showsSilenceNote(0), false, "the empty state says it itself");
  assert.equal(showsSilenceNote(1), true);
  assert.equal(showsSilenceNote(SILENCE_NOTE_UNTIL - 1), true);
  assert.equal(showsSilenceNote(SILENCE_NOTE_UNTIL), false);
  assert.equal(showsSilenceNote(12), false);
});

// A minimal DOM keeps these interaction tests independent of browser tooling.
class TestNode {
  children = [];
  listeners = {};
  dataset = {};
  style = {};
  className = "";
  value = "";
  textContent = "";
  classList = {
    contains: (name) => this.className.split(" ").includes(name),
    toggle: (name, on) => {
      const names = new Set(this.className.split(" ").filter(Boolean));
      if (on) names.add(name); else names.delete(name);
      this.className = [...names].join(" ");
    },
    remove: (name) => this.classList.toggle(name, false),
  };
  append(...nodes) {
    for (const node of nodes) {
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
    if (name === "data-reply-to") this.dataset.replyTo = value;
    else this[name] = value;
  }
  addEventListener(name, fn) { (this.listeners[name] ??= []).push(fn); }
  fire(name, event = {}) { for (const fn of this.listeners[name] || []) fn(event); }
  focus() { document.activeElement = this; }
  setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; }
  querySelectorAll(selector) {
    return this.children.flatMap((child) => [
      ...(child.classList.contains(selector.slice(1)) ? [child] : []),
      ...child.querySelectorAll(selector),
    ]);
  }
}

test("reply dictation survives updates, sends to its target, and stops when closed", async (t) => {
  t.mock.method(globalThis, "setTimeout", () => 0);
  t.mock.method(api, "reply", async () => ({}));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  globalThis.cancelAnimationFrame = () => {};
  const recognizers = [];
  globalThis.SpeechRecognition = class {
    constructor() { recognizers.push(this); }
    start() {}
    stop() { this.stopped = true; }
    abort() {}
    result(text) {
      const result = [{ transcript: text }];
      result.isFinal = true;
      this.onresult({ results: [result] });
    }
  };
  t.after(() => {
    delete globalThis.Node;
    delete globalThis.document;
    delete globalThis.cancelAnimationFrame;
    delete globalThis.SpeechRecognition;
  });
  const feed = createFeed({
    getPersona: () => null,
    getSettings: () => ({ speech: { provider: "browser", language: "ja", sendPhrase: "送信" } }),
  });
  const post = { id: "post-1", kind: "post", text: "hello", at: new Date().toISOString() };
  const button = (label) => {
    const walk = (node) => [node, ...node.children.flatMap(walk)];
    const found = walk(feed.element).find((node) => node.textContent === translate(label));
    assert.ok(found, label);
    return found;
  };
  feed.set([post]);
  button("一言足す").fire("click");
  button("🎙 話す").fire("click");
  const first = recognizers[0];
  assert.equal(first.lang, "ja-JP");
  first.result("音声の返事");
  const input = feed.element.querySelectorAll(".reply-input")[0];
  assert.equal(input.value, "音声の返事");
  feed.upsert({ id: "remark", kind: "remark", root: post.id, speaker: "bot", text: "reply", at: post.at });
  assert.equal(feed.element.querySelectorAll(".reply-input")[0], input);
  first.result("音声の返事です送信");
  await new Promise(setImmediate);
  assert.deepEqual(api.reply.mock.calls[0].arguments, [post.id, "音声の返事です"]);
  assert.equal(first.stopped, true);
  assert.equal(feed.element.querySelectorAll(".reply-input").length, 0);

  button("一言足す").fire("click");
  button("🎙 話す").fire("click");
  const second = recognizers[1];
  second.result("途中");
  button("返す").fire("click");
  assert.equal(second.stopped, true, "switching reply target stops recognition");
  button("🎙 話す").fire("click");
  const third = recognizers[2];
  button("やめる").fire("click");
  assert.equal(third.stopped, true);
  assert.equal(feed.element.querySelectorAll(".reply-input").length, 0);

  button("一言足す").fire("click");
  assert.equal(feed.element.querySelectorAll(".reply-input")[0].value, "途中");
  button("🎙 話す").fire("click");
  const fourth = recognizers[3];
  feed.set([]);
  assert.equal(fourth.stopped, true, "removing the thread stops recognition");
});

test("delete updates the displayed thread from the API response without SSE", async (t) => {
  const post = { id: "delete-post", kind: "post", root: "delete-post", text: "remove me", at: new Date().toISOString() };
  t.mock.method(api, "remove", async () => ({ ...post, deleted: true }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  globalThis.confirm = () => true;
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.confirm; });
  let update;
  const feed = createFeed({ getPersona: () => null, onEntryChange: (entry) => { update = entry; feed.upsert(entry); } });
  feed.set([post, { id: "reply", kind: "reply", root: post.id, at: post.at, text: "reply" }]);
  const button = feed.element.querySelectorAll(".remove")[0];
  button.fire("click", { currentTarget: button });
  assert.equal(button.disabled, true);
  await new Promise(setImmediate);
  assert.equal(update.deleted, true);
  assert.equal(feed.element.querySelectorAll(".thread").length, 0);
  assert.equal(button.disabled, false);
});

test("a failed delete keeps the post and allows retry", async (t) => {
  t.mock.method(api, "remove", async () => { throw Error("offline"); });
  t.mock.method(globalThis, "setTimeout", () => 0);
  globalThis.Node = TestNode;
  const toasts = new TestNode();
  globalThis.document = { createElement: () => new TestNode(), activeElement: null, querySelector: () => toasts };
  globalThis.confirm = () => true;
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.confirm; });
  const feed = createFeed({ getPersona: () => null });
  feed.set([{ id: "keep", kind: "post", root: "keep", text: "keep me", at: new Date().toISOString() }]);
  const button = feed.element.querySelectorAll(".remove")[0];
  button.fire("click", { currentTarget: button });
  await new Promise(setImmediate);
  assert.equal(feed.element.querySelectorAll(".thread").length, 1);
  assert.equal(button.disabled, false);
  assert.equal(toasts.children[0].textContent, "offline");
});
