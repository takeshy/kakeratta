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
  constructor(tag) { if (tag) this.tagName = String(tag).toUpperCase(); }
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
  replaceChildren(...nodes) {
    while (this.firstChild) this.firstChild.remove();
    this.append(...nodes);
  }
  remove() {
    if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1);
    this.parent = null;
  }
  get firstChild() { return this.children[0]; }
  get isConnected() {
    let node = this;
    while (node.parent) node = node.parent;
    return node === globalThis.document?.body;
  }
  setAttribute(name, value) {
    if (name === "disabled") this.disabled = true;
    else if (name === "data-pending") this.dataset.pending = value;
    else if (name === "data-thread") this.dataset.thread = value;
    else if (name === "data-reply-to") this.dataset.replyTo = value;
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

test("persona HTML renders in the feed while user HTML stays literal", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: tag => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const html = '<!DOCTYPE html><html><body><h1>図解</h1></body></html>';
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  feed.set([
    { id: 'html-persona', kind: 'post', speaker: 'infographic', text: html, at: '2026-10-02T00:00:00Z' },
    { id: 'html-user', kind: 'post', speaker: 'user', text: html, at: '2026-10-02T00:01:00Z' },
  ]);
  const frames = feed.element.querySelectorAll('.html-document');
  assert.equal(frames.length, 1);
  assert.equal(frames[0].sandbox, '');
  assert.ok(frames[0].srcdoc.includes(html));
  assert.ok(feed.element.querySelectorAll('.said').some(node => node.tagName === 'P' && node.textContent === html));
});

test("infographic Markdown is laid out as an infographic; its prose and other personas stay Markdown", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: tag => new TestNode(tag), createElementNS: (_, tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const markdown = '# 📊 売上\n昨年の2倍。\n\n## 📈 推移\n- 🗓️ 昨年\n- 🚀 今年';
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  feed.set([
    { id: 'graphic', kind: 'post', speaker: 'infographic', text: markdown, at: '2026-10-02T00:00:00Z' },
    { id: 'question', kind: 'post', speaker: 'infographic', text: '本文を貼ってください。', at: '2026-10-02T00:01:00Z' },
    { id: 'other', kind: 'post', speaker: 'gourmet', text: markdown, at: '2026-10-02T00:02:00Z' },
  ]);
  assert.equal(feed.element.querySelectorAll('.said-infographic').length, 1);
  assert.equal(feed.element.querySelectorAll('.infographic-hero').length, 1);
  assert.equal(feed.element.querySelectorAll('.infographic-section').length, 1);
  assert.equal(feed.element.querySelectorAll('.said-markdown').length, 3);
});

test("search reveals a folded reply and polling preserves a later manual fold", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  const entries = [
    { id: "p", kind: "post", text: "wine", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "reply", root: "p", text: "Pinot", at: "2026-09-13T00:01:00Z" },
  ];
  feed.set(entries);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 0);
  feed.reveal("r");
  assert.equal(feed.element.querySelectorAll(".said-row").length, 1);
  assert.equal(feed.element.querySelectorAll(".note").length, 0);
  feed.set([...entries]);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 1);
  feed.element.querySelectorAll(".thread-toggle")[0].fire("click");
  feed.set([...entries]);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 0);
});

// A URL dropped into a reply is read the same way as one in a post: it must
// still show up as a link a person can open in another tab, not just as text.
test("a reply's link renders as a link that opens in a new tab", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  const entries = [
    { id: "p", kind: "post", text: "wine", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "reply", root: "p", text: "これ見て http://127.0.0.1/a", link: "http://127.0.0.1/a", at: "2026-09-13T00:01:00Z" },
  ];
  feed.set(entries);
  feed.reveal("r");
  const [link] = feed.element.querySelectorAll(".link");
  assert.equal(link?.href, "http://127.0.0.1/a");
  assert.equal(link?.target, "_blank");
});

// An answer on Ultra is marked, so a reply to "もっと詳しく" can be told
// apart from an ordinary one. Super is the default and goes unmarked, as does
// a remark with no model (the user's own words, a failed turn).
test("only a remark answered on Ultra carries the Ultra badge", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  const at = "2026-09-14T00:00:00Z";
  const said = [
    { id: "s", kind: "remark", root: "p", speaker: "gourmet", model: "super", text: "豚骨", at },
    { id: "u", kind: "reply", root: "p", speaker: "user", text: "もっと詳しく", at },
    { id: "d", kind: "remark", root: "p", speaker: "gourmet", model: "ultra", text: "## 豚骨", at },
    { id: "f", kind: "remark", root: "p", speaker: "move", text: "今はうまく答えられませんでした。", at },
  ];
  feed.set([{ id: "p", kind: "post", speaker: "user", text: "ラーメン", at }, ...said]);
  feed.reveal("f");
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const badgesOf = (id) => {
    const row = walk(feed.element).find((node) => node.attributes?.["data-id"] === id || node["data-id"] === id || node.dataset?.id === id);
    assert.ok(row, `row ${id} is drawn`);
    return walk(row).filter((node) => String(node.className).includes("model-badge"));
  };
  for (const entry of said) {
    const badges = badgesOf(entry.id);
    if (entry.model === "ultra") {
      assert.equal(badges.length, 1, `${entry.id} carries the badge`);
      assert.equal(badges[0].textContent, "Ultra");
    } else {
      assert.equal(badges.length, 0, `${entry.id} carries no badge`);
    }
  }
});

// A reply from the external agent to a request made to it directly has no
// persona row: it is drawn under the agent's own name, rendered as Markdown.
test("the external agent's direct reply is drawn under its own name", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => undefined, showIntro: false });
  const at = "2026-09-14T00:00:00Z";
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "@外部エージェント メールして", at },
    { id: "a", kind: "reply", root: "p", replyTo: "p", speaker: "agent", text: "**送信しました**", at },
  ]);
  feed.reveal("a");
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const row = walk(feed.element).find((node) => node["data-id"] === "a" || node.dataset?.id === "a");
  assert.ok(row, "the reply is drawn");
  const nodes = walk(row);
  assert.ok(nodes.some((node) => node.textContent === translate("外部エージェント")), "named as the external agent");
  assert.ok(!nodes.some((node) => node.textContent === "agent"), "not by its speaker id");
  assert.ok(nodes.some((node) => String(node.className).includes("said-markdown")));
  feed.setRequests([], "Hermes");
  const named = walk(walk(feed.element).find((node) => node["data-id"] === "a" || node.dataset?.id === "a"));
  assert.ok(named.some((node) => node.textContent === "Hermes"), "named by its connection");
});

// A post asked of the external agent says whether it waits or is being worked
// on, until the request ends, and its bell chooses whether its end is pushed.
test("a direct request shows whether the agent has it", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => undefined, showIntro: false });
  const at = "2026-09-14T00:00:00Z";
  feed.set([{ id: "p", kind: "post", speaker: "user", text: "@agent メールして", at }]);
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const badge = () => walk(feed.element).find((node) => String(node.className).includes("request-badge"));
  assert.equal(badge(), undefined);
  const bell = () => walk(feed.element).find((node) => String(node.className).includes("request-notify"));
  feed.setRequests([{ id: "q1", entryId: "p", status: "pending", notify: true }]);
  assert.equal(badge()?.textContent, translate("外部エージェントに依頼中"));
  assert.equal(bell()?.textContent, "🔔");
  const saved = [];
  t.mock.method(api, "setRequestNotify", async (id, notify) => { saved.push([id, notify]); return { id, entryId: "p", status: "pending", notify }; });
  const button = bell();
  await button.listeners.click[0]({ stopPropagation() {}, currentTarget: button });
  assert.deepEqual(saved, [["q1", false]]);
  assert.equal(bell()?.textContent, "🔕");
  feed.request({ id: "q1", entryId: "p", state: "claimed", notify: false });
  assert.equal(badge()?.textContent, translate("外部エージェントが処理中"));
  assert.equal(bell()?.textContent, "🔕");
  feed.request({ id: "q1", entryId: "p", state: "replied" });
  assert.equal(badge(), undefined);
  assert.equal(bell(), undefined);
});

// A persona's reply is rendered, because nobody edits it: emphasis, deletion,
// code and links read as themselves, and a javascript: link stays inert. Your
// own words stay exactly as typed until the setting asks for Markdown.
test("personas render as markdown, user text stays plain until asked", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let settings = {};
  const feed = createFeed({ getPersona: () => null, getSettings: () => settings, showIntro: false });
  const at = "2026-09-14T00:00:00Z";
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "plain **stay**", at },
    { id: "r", kind: "reply", root: "p", speaker: "tech", at, text: "**bold** ~~gone~~ `code` [ok](https://example.com/x) [bad](javascript:alert(1))" },
  ]);
  feed.reveal("r");
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const nodes = () => walk(feed.element);
  const tag = (name, text) => (node) => node.tagName === name && node.textContent === text;
  assert.ok(nodes().some(tag("STRONG", "bold")), "emphasis renders");
  assert.ok(nodes().some(tag("DEL", "gone")), "deletion renders");
  assert.ok(nodes().some(tag("CODE", "code")), "code renders");
  assert.deepEqual(nodes().filter((node) => node.tagName === "A").map((node) => node.href), ["https://example.com/x"]);
  // The user's own words stay as typed.
  assert.ok(nodes().some(tag("P", "plain **stay**")));
  assert.ok(!nodes().some(tag("STRONG", "stay")));
  // The setting asks for the user's Markdown to be honoured; editing keeps
  // meaning the source, so the same text now renders.
  settings = { markdownInput: true };
  feed.draw();
  assert.ok(nodes().some(tag("STRONG", "stay")));
  assert.ok(!nodes().some(tag("P", "plain **stay**")));
});

// The person watching a folded timeline cannot tell that a persona has just
// spoken. A remark that arrives after the timeline is showing unfolds its
// post; the first load and older pages do not, and a fold closed by hand stays
// closed until the next remark.
test("a persona's remark arriving unfolds its post; loads and older pages do not", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  const said = (count) => feed.element.querySelectorAll(".said-row").length === count;
  const old = [
    { id: "p1", kind: "post", speaker: "user", text: "old", at: "2026-09-13T00:00:00Z" },
    { id: "r1", kind: "reply", root: "p1", speaker: "tech", text: "old remark", at: "2026-09-13T00:01:00Z" },
  ];
  const post = { id: "p2", kind: "post", speaker: "user", text: "wine", at: "2026-09-14T00:00:00Z" };
  // First load: everything stays folded.
  feed.set([post, ...old]);
  assert.ok(said(0), "the first load leaves folds closed");
  // A poll brings a persona's remark: its post opens.
  const remark = { id: "r2", kind: "reply", root: "p2", speaker: "tech", text: "Pinot", at: "2026-09-14T00:03:00Z" };
  feed.set([post, remark, ...old]);
  assert.ok(said(1), "the arriving remark is shown");
  // Folding by hand holds through the next poll with nothing new.
  feed.element.querySelectorAll(".thread-toggle")[0].fire("click");
  feed.set([post, remark, ...old]);
  assert.ok(said(0), "a fold closed by hand stays closed");
  // The live channel brings another remark to the same post: it opens again.
  feed.upsert({ id: "r3", kind: "reply", root: "p2", speaker: "wine", text: "Or a Gamay", at: "2026-09-14T00:04:00Z" });
  assert.ok(said(2), "a remark over the live channel opens the post again");
  // An older page arriving under the timeline opens nothing.
  const older = [
    { id: "p0", kind: "post", speaker: "user", text: "older", at: "2026-09-12T00:00:00Z" },
    { id: "r0", kind: "reply", root: "p0", speaker: "tech", text: "older remark", at: "2026-09-12T00:01:00Z" },
  ];
  feed.set([post, remark, { id: "r3", kind: "reply", root: "p2", speaker: "wine", text: "Or a Gamay", at: "2026-09-14T00:04:00Z" }, ...old, ...older]);
  assert.ok(said(2), "an older page does not unfold its posts");
  // The user's own reply from elsewhere is not a persona speaking.
  feed.upsert({ id: "u1", kind: "reply", root: "p1", speaker: "user", text: "mine", at: "2026-09-14T00:05:00Z" });
  assert.ok(said(2), "the user's own reply does not unfold a post");
});

test("reply dictation survives updates, sends to its target, and stops when closed", async (t) => {
  t.mock.method(globalThis, "setTimeout", () => 0);
  t.mock.method(api, "reply", async () => ({}));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  globalThis.cancelAnimationFrame = () => {};
  const recognizers = [];
  globalThis.SpeechRecognition = class {
    constructor() { recognizers.push(this); }
    start() { this.onstart?.(); }
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
    getSettings: () => ({ speech: { provider: "live", language: "ja", sendPhrase: "送信" } }),
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
  assert.deepEqual(api.reply.mock.calls[0].arguments, [post.id, "音声の返事です", true]);
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
  document.activeElement = null;
  feed.set([post]);
  const manual = feed.element.querySelectorAll(".reply-input")[0];
  manual.value = "手動で送ります";
  manual.fire("keydown", { ctrlKey: true, key: "Enter", preventDefault() {} });
  await new Promise(setImmediate);
  assert.deepEqual(api.reply.mock.calls[1].arguments, [post.id, "手動で送ります", false]);
});

test("delete updates the displayed thread from the API response without SSE", async (t) => {
  const post = { id: "delete-post", kind: "post", root: "delete-post", text: "remove me", at: new Date().toISOString() };
  t.mock.method(api, "remove", async () => ({ ...post, deleted: true }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
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
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null, querySelector: () => toasts };
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

test("the user's own reply can be deleted", async (t) => {
  const post = { id: "p", kind: "post", speaker: "user", root: "p", text: "root", at: "2026-09-13T00:00:00Z" };
  const reply = { id: "u", kind: "reply", speaker: "user", root: "p", replyTo: "p", text: "my reply", at: "2026-09-13T00:01:00Z" };
  t.mock.method(api, "remove", async () => ({ ...reply, deleted: true }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  globalThis.confirm = () => true;
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.confirm; });
  let feed;
  feed = createFeed({ getPersona: () => null, onEntryChange: (entry) => feed.upsert(entry), showIntro: false });
  feed.set([post, reply]);
  feed.reveal(reply.id);
  const buttons = feed.element.querySelectorAll(".remove");
  assert.equal(buttons.length, 2, "the post and the user's reply both have delete controls");
  buttons[1].fire("click", { currentTarget: buttons[1] });
  await new Promise(setImmediate);
  assert.equal(api.remove.mock.calls[0].arguments[0], reply.id);
  assert.equal(feed.element.querySelectorAll(".said-row.mine").length, 0);
  assert.equal(feed.element.querySelectorAll(".thread").length, 1);
});

test("a persona's answer can be deleted, keeping the thread", async (t) => {
  const post = { id: "p", kind: "post", speaker: "user", root: "p", text: "root", at: "2026-09-13T00:00:00Z" };
  const remark = { id: "r", kind: "reply", speaker: "gourmet", root: "p", replyTo: "p", text: "wrong answer", at: "2026-09-13T00:01:00Z" };
  t.mock.method(api, "remove", async () => ({ ...remark, deleted: true }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  let asked = "";
  globalThis.confirm = (message) => { asked = message; return true; };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.confirm; });
  let feed;
  feed = createFeed({ getPersona: () => ({ id: "gourmet", name: "tabe" }), onEntryChange: (entry) => feed.upsert(entry), showIntro: false });
  feed.set([post, remark]);
  feed.reveal(remark.id);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 1);
  const buttons = feed.element.querySelectorAll(".remove");
  assert.equal(buttons.length, 2, "the post and the persona's answer both have delete controls");
  buttons[1].fire("click", { currentTarget: buttons[1] });
  await new Promise(setImmediate);
  assert.match(asked, /一緒に書いたメモは残ります|Notes written with it are kept/);
  assert.equal(api.remove.mock.calls[0].arguments[0], remark.id);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 0);
  assert.equal(feed.element.querySelectorAll(".thread").length, 1);
});

test("deleting an answer takes the replies under it, and theirs", async (t) => {
  const post = { id: "p", kind: "post", speaker: "user", root: "p", text: "root", at: "2026-09-13T00:00:00Z" };
  const remark = { id: "r", kind: "reply", speaker: "gourmet", root: "p", replyTo: "p", text: "wrong answer", at: "2026-09-13T00:01:00Z" };
  const mine = { id: "u", kind: "reply", speaker: "user", root: "p", replyTo: "r", text: "that is wrong", at: "2026-09-13T00:02:00Z" };
  const again = { id: "r2", kind: "reply", speaker: "gourmet", root: "p", replyTo: "u", text: "still wrong", at: "2026-09-13T00:03:00Z" };
  const other = { id: "s", kind: "reply", speaker: "move", root: "p", replyTo: "p", text: "unrelated", at: "2026-09-13T00:04:00Z" };
  t.mock.method(api, "remove", async () => ({ ...remark, deleted: true }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  let asked = "";
  globalThis.confirm = (message) => { asked = message; return true; };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.confirm; });
  let feed;
  feed = createFeed({ getPersona: (id) => ({ id, name: id }), onEntryChange: (entry) => feed.upsert(entry), showIntro: false });
  feed.set([post, remark, mine, again, other]);
  feed.reveal(other.id);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 4);
  const buttons = feed.element.querySelectorAll(".remove");
  buttons[1].fire("click", { currentTarget: buttons[1] });
  await new Promise(setImmediate);
  assert.match(asked, /2/);
  assert.equal(api.remove.mock.calls.length, 1, "the server takes the replies itself");
  assert.equal(api.remove.mock.calls[0].arguments[0], remark.id);
  assert.equal(feed.element.querySelectorAll(".said-row").length, 1, "only the unrelated remark is left");
});

test("persona builder dictates with saved settings, sends, and stops on close", async (t) => {
  const { showPersonaBuilder } = await import("../frontend/src/persona-builder.js");
  t.mock.method(api, "buildPersona", async () => ({ message: "続けて教えてください", draft: null }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null };
  globalThis.cancelAnimationFrame = () => {};
  const recognizers = [];
  globalThis.SpeechRecognition = class {
    constructor() { recognizers.push(this); }
    start() { this.onstart?.(); }
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
  showPersonaBuilder({ getSettings: () => ({ speech: { language: "ja", sendPhrase: "送信" } }) });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const button = label => walk(document.body).find(node => node.textContent === translate(label));
  const input = document.body.querySelectorAll(".skill-input").find(node => !node.className.includes("person-basic-info"));
  button("🎙 話す").fire("click");
  assert.equal(recognizers[0].lang, "ja-JP");
  recognizers[0].result("ワインの担当を作りたい");
  assert.equal(input.value, "ワインの担当を作りたい");
  recognizers[0].result("ワインの担当を作りたい送信");
  await new Promise(setImmediate);
  assert.equal(api.buildPersona.mock.callCount(), 1);
  assert.deepEqual(api.buildPersona.mock.calls[0].arguments[0].messages, [
    { role: "assistant", content: translate("どんなことの担当を作りたい？") },
    { role: "user", content: "ワインの担当を作りたい" },
  ]);
  assert.equal(recognizers[0].stopped, true);
  button("🎙 話す").fire("click");
  walk(document.body).find(node => node.className.includes("dialog-close")).fire("click");
  assert.equal(recognizers[1].stopped, true);
  assert.equal(document.body.children.length, 0);
});


// Personal information is editable and saved in the definition without a post.
test("person creation saves editable basic information without posting", async (t) => {
  const { showPersonaBuilder } = await import("../frontend/src/persona-builder.js");
  const nagomi = { id: "nagomi", name: "nagomi", icon: "🌸", domain: "nagomi本人のこと。", tone: "短く", memory: "Fact と Event", instructions: "覚える", tools: ["schedule"], kind: "person", aliases: ["なごみ", "恋人"], keywords: [] };
  t.mock.method(api, "buildPersona", async () => ({ message: "案です", draft: { ...nagomi, basicInfo: "nagomiは1972/11/10生まれ。タイ料理が好き。" }, checkIn: "monthly" }));
  t.mock.method(api, "setDailyCheckIn", async () => ({ personas: [] }));
  t.mock.method(api, "savePersonaDraft", async (skill) => ({ ...skill, active: true }));
  t.mock.method(api, "post", async (body) => ({ id: "f1", kind: "post", text: body.text }));
  t.mock.method(globalThis, "setTimeout", () => 0);
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const posted = [];
  let saved = null;
  showPersonaBuilder({ onSave: (skill) => { saved = skill; }, onPosted: (entry) => posted.push(entry) });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const button = label => walk(document.body).find(node => node.textContent === translate(label) && node.listeners.click);
  const input = document.body.querySelectorAll(".skill-input").find(node => !node.className.includes("person-basic-info"));
  input.value = "恋人のnagomiのことを覚えて。1972/11/10生まれ。タイ料理が好き。";
  walk(document.body).find(node => node.listeners.submit).fire("submit", { preventDefault() {} });
  await new Promise(setImmediate);
  const box = document.body.querySelectorAll(".person-basic-info")[0];
  assert.equal(box.value, "nagomiは1972/11/10生まれ。タイ料理が好き。");
  assert.equal(document.body.querySelectorAll(".person-basic-info-field")[0].hidden, false);
  assert.equal(document.body.querySelectorAll(".person-basic-info-field")[0].querySelectorAll(".switch").length, 0);
  box.value = "nagomiは1972/11/10生まれ。辛いタイ料理が好き。";
  button("この担当を追加").fire("click");
  for (let i = 0; i < 6; i++) await new Promise(setImmediate);
  assert.equal(saved?.id, "nagomi");
  assert.equal(saved.basicInfo, "nagomiは1972/11/10生まれ。辛いタイ料理が好き。");
  assert.equal(api.post.mock.calls.length, 0);
  assert.equal(posted.length, 0);
  // The AI chose to look back on her monthly; saving says so.
  assert.deepEqual(api.setDailyCheckIn.mock.calls[0].arguments, ["nagomi", { talk: "share", every: "monthly" }]);
  // The creation checkbox overrides the generated draft and suppresses the
  // AI's proposed monthly check-in when remembering is switched off.
  showPersonaBuilder({ onSave: skill => { saved = skill; } });
  const toggle = document.body.querySelectorAll(".persona-memory-enabled")[0];
  assert.equal(toggle.checked, true);
  toggle.checked = false;
  toggle.fire("change");
  const ultra = document.body.querySelectorAll(".persona-always-ultra")[0];
  assert.equal(ultra.checked, false);
  ultra.checked = true;
  ultra.fire("change");
  document.body.querySelectorAll(".skill-input").find(node => !node.className.includes("person-basic-info")).value = "記憶せずに回答だけして";
  walk(document.body).find(node => node.listeners.submit).fire("submit", { preventDefault() {} });
  await new Promise(setImmediate);
  button("この担当を追加").fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  assert.equal(saved.memoryEnabled, false);
  assert.equal(saved.alwaysUltra, true);
  assert.equal(api.setDailyCheckIn.mock.callCount(), 1, "no check-in was saved for the memoryless draft");



});

// The month's summary is the app's post, with an edit button like the user's.
test("the month's summary is shown as the app's and can be edited", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  feed.set([{ id: "m", kind: "post", speaker: "kakeratta", summary: "month", root: "m", text: "### 人\n- 佐藤さんと会った", at: "2026-10-12T09:00:00Z" }]);
  const walk = node => [node, ...node.children.flatMap(walk)];
  assert.ok(walk(feed.element).some(node => node.textContent === translate("今月のまとめ")));
  assert.equal(feed.element.querySelectorAll(".edit-post").length, 1);
  assert.equal(feed.element.querySelectorAll(".routing-open").length, 0, "the router never judged it");
});


test("read aloud selects only one thread, including user replies, and updates its stop button", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let request;
  const feed = createFeed({ getPersona: () => ({ name: "ワイン係" }), onSpeak: (...args) => { request = args; } });
  const at = "2026-09-13T00:00:00Z";
  feed.set([
    { id: "p", kind: "post", text: "ワイン", at },
    { id: "r", root: "p", kind: "remark", speaker: "wine", text: "赤です", at },
    { id: "u", root: "p", kind: "reply", speaker: "user", text: "ありがとう", at },
    { id: "other", kind: "post", text: "別の話", at },
  ]);
  const button = feed.element.querySelectorAll(".thread-speak").find(node => node.dataset.thread === "p");
  button.fire("click");
  assert.equal(request[0], "p");
  assert.deepEqual(request[1].map(line => line.id), ["p", "r", "u"]);
  feed.setSpeaking("p");
  assert.equal(button.textContent, translate("■ 停止"));
  feed.draw();
  assert.equal(feed.element.querySelectorAll(".thread-speak").find(node => node.dataset.thread === "p").textContent, translate("■ 停止"));
  feed.setSpeaking(null);
  assert.ok(feed.element.querySelectorAll(".thread-speak").every(node => node.textContent === translate("🔈 読み上げ")));
  assert.deepEqual(feed.spoken("other").map(line => line.id), ["other"]);
});

test("persona bar starts fully collapsed and expands every badge for selection", async t => {
  const { createRoom } = await import("../frontend/src/room.js");
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let selected;
  const room = createRoom({ onSelect: row => { selected = row.id; } });
  room.set(Array.from({ length: 6 }, (_, i) => ({ id: String(i), name: `担当${i}`, active: true })));
  const more = room.bar.querySelectorAll(".persona-more")[0];
  assert.equal(more.textContent, translate("担当を表示"));
  assert.equal(room.bar.querySelectorAll(".persona-chip").length, 0);
  more.fire("click");
  assert.equal(room.bar.classList.contains("expanded"), true);
  assert.equal(room.bar.querySelectorAll(".persona-chip").length, 6);
  room.bar.querySelectorAll(".persona-chip")[2].fire("click");
  assert.equal(selected, "2");
  room.bar.querySelectorAll(".persona-more")[0].fire("click");
  assert.equal(room.bar.classList.contains("expanded"), false);
  assert.equal(room.bar.querySelectorAll(".persona-chip").length, 0);
  room.set([{ id: "one", name: "担当", active: true }]);
  assert.equal(room.bar.querySelectorAll(".persona-more").length, 1);
  assert.equal(room.bar.querySelectorAll(".persona-chip").length, 0);
});

test("daily check-ins display their persona and sources as replyable parent posts", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => ({ name: "Tech", icon: "💻" }), showIntro: false });
  feed.set([{ id: "daily", root: "daily", kind: "post", speaker: "tech", text: "A relevant change", at: "2026-09-14T00:00:00Z", sources: [{ label: "Source", url: "https://example.com/news" }] }]);
  assert.equal(feed.element.querySelectorAll(".who")[0].textContent, "Tech");
  assert.equal(feed.element.querySelectorAll(".avatar")[0].textContent, "💻");
  assert.equal(feed.element.querySelectorAll(".source")[0].href, "https://example.com/news");
  assert.equal(feed.element.querySelectorAll(".thread").length, 1);
});

test("compose stops dictation and resets its button on manual and spoken send", async (t) => {
  const { createCompose } = await import("../frontend/src/compose.js");
  t.mock.method(api, "post", async () => ({}));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  globalThis.cancelAnimationFrame = () => {};
  const recognizers = [];
  globalThis.SpeechRecognition = class {
    constructor() { recognizers.push(this); }
    start() { this.onstart?.(); }
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
  const compose = createCompose({ getSettings: () => ({ speech: { language: "ja", sendPhrase: "送信" } }) });
  compose.setPlace({ lat: 35, lon: 139 });
  const mic = compose.element.querySelectorAll(".voice-button")[0];
  const input = compose.element.querySelectorAll(".compose-text")[0];
  for (const spoken of [false, true]) {
    mic.fire("click");
    assert.equal(mic.classList.contains("active"), true);
    assert.equal(mic["aria-pressed"], "true");
    const recognition = recognizers.at(-1);
    recognition.result(spoken ? "かけら送信" : "かけら");
    if (!spoken) compose.element.fire("submit", { preventDefault() {} });
    assert.equal(recognition.stopped, true);
    assert.equal(mic.classList.contains("active"), false);
    assert.equal(mic["aria-pressed"], "false");
    assert.equal(recognition.onresult, null);
    await new Promise(setImmediate);
    assert.equal(input.value, "");
    assert.equal(api.post.mock.calls.at(-1).arguments[0].text, "かけら");
    assert.equal(api.post.mock.calls.at(-1).arguments[0].speechAutoSend, spoken);
  }
  assert.equal(api.post.mock.calls.length, 2);
});

test("post editing preserves drafts across updates, handles failure, and saves or cancels", async t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const post = { id: "edit", kind: "post", speaker: "user", text: "before", at: new Date().toISOString() };
  const feed = createFeed({ getPersona: () => null });
  feed.set([post]);
  const find = name => feed.element.querySelectorAll(`.${name}`)[0];
  find("edit-post").fire("click");
  const input = find("edit-input");
  input.value = "after";
  input.setSelectionRange(2, 2);
  feed.set([{ ...post }]);
  assert.equal(find("edit-input"), input);
  assert.equal(input.value, "after");
  assert.equal(document.activeElement, input);
  t.mock.method(api, "edit", async () => { throw Error("offline"); });
  find("save-edit").fire("click");
  await new Promise(setImmediate);
  assert.equal(find("edit-input").value, "after");
  assert.equal(find("error").textContent, "offline");
  assert.equal(find("save-edit").disabled, false);
  api.edit.mock.mockImplementation(async (id, text) => ({ ...post, id, text }));
  find("save-edit").fire("click");
  await new Promise(setImmediate);
  assert.deepEqual(api.edit.mock.calls.at(-1).arguments, [post.id, "after"]);
  assert.equal(find("edit-input"), undefined);
  assert.equal(find("said").textContent, "after");
  find("edit-post").fire("click");
  find("edit-input").value = "discard";
  find("cancel-edit").fire("click");
  assert.equal(find("said").textContent, "after");
  assert.equal(api.edit.mock.calls.length, 2);
  feed.set([{ ...post, speaker: "tabe" }]);
  assert.equal(find("edit-post"), undefined);
});

test("editing is disabled offline and an open draft can be saved after reconnecting", async t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  const originalNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  const originalListener = Object.getOwnPropertyDescriptor(globalThis, "addEventListener");
  const network = { onLine: false };
  const events = {};
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: network });
  Object.defineProperty(globalThis, "addEventListener", { configurable: true, value: (name, fn) => { events[name] = fn; } });
  t.after(() => {
    delete globalThis.Node; delete globalThis.document;
    if (originalNavigator) Object.defineProperty(globalThis, "navigator", originalNavigator); else delete globalThis.navigator;
    if (originalListener) Object.defineProperty(globalThis, "addEventListener", originalListener); else delete globalThis.addEventListener;
  });
  const post = { id: "offline-edit", speaker: "user", kind: "post", text: "before", at: new Date().toISOString() };
  t.mock.method(api, "edit", async (id, text) => ({ ...post, id, text }));
  const feed = createFeed({ getPersona: () => null });
  feed.set([post]);
  const find = name => feed.element.querySelectorAll(`.${name}`)[0];
  assert.equal(find("edit-post").disabled, true);
  find("edit-post").fire("click");
  assert.equal(find("edit-input"), undefined);
  network.onLine = true;
  events.online();
  assert.equal(find("edit-post").disabled, false);
  find("edit-post").fire("click");
  const input = find("edit-input");
  input.value = "draft";
  network.onLine = false;
  events.offline();
  assert.equal(find("save-edit").disabled, true);
  input.fire("keydown", { ctrlKey: true, key: "Enter", preventDefault() {} });
  assert.equal(api.edit.mock.calls.length, 0);
  assert.equal(input.value, "draft");
  network.onLine = true;
  events.online();
  assert.equal(find("save-edit").disabled, false);
  find("save-edit").fire("click");
  await new Promise(setImmediate);
  assert.equal(find("said").textContent, "draft");
});

test("persona editor separates direct editing from AI editing", async (t) => {
  const { showPersonaEditor } = await import("../frontend/src/persona-editor.js");
  const persona = { id: "move", name: "michi", icon: "🚃", domain: "移動", tone: "短く", instructions: "場所を覚える", tools: ["place_search"] };
  t.mock.method(api, "buildPersona", async (body) => ({ message: "短くしました", draft: { ...body.current, tone: "一言だけ" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  globalThis.cancelAnimationFrame = () => {};
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.cancelAnimationFrame; });

  let saved = false;
  showPersonaEditor({ persona, toolNames: ["web_search", "place_search", "schedule"], onSave: () => { saved = true; } });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const button = label => walk(document.body).find(node => node.textContent === translate(label));
  const inputs = document.body.querySelectorAll(".skill-input");
  const name = inputs.find(node => node.value === "michi");
  const tone = inputs.find(node => node.value === "短く");

  // Direct editing has no AI request mixed into it.
  assert.equal(document.body.querySelectorAll(".ai-request").length, 0);
  const memoryToggle = document.body.querySelectorAll(".persona-memory-enabled")[0];
  const ultraToggle = document.body.querySelectorAll(".persona-always-ultra")[0];
  assert.equal(ultraToggle.checked, false);
  ultraToggle.checked = true;
  assert.equal(memoryToggle.checked, true, "existing definitions remember by default");
  memoryToggle.checked = false;
  memoryToggle.fire("change");
  name.value = "みち";
  name.fire("input");
  const previous = document.body.querySelectorAll(".previous");
  assert.ok(previous.some(node => !node.hidden && walk(node).some(child => child.textContent === "michi")), "the original name is shown next to the edit");

  button("保存").fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[0].name, "みち");
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[0].memoryEnabled, false);
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[0].alwaysUltra, true);
  assert.ok(saved);

  // AI editing opens separately. Current values are reference-only until the
  // proposal arrives, then the proposal can be adjusted before saving.
  showPersonaEditor({ persona, toolNames: ["web_search", "place_search", "schedule"], ai: true });
  const aiDialog = document.body.querySelectorAll(".persona-editor").at(-1);
  const aiInputs = aiDialog.querySelectorAll(".skill-input");
  const aiName = aiInputs.find(node => node.value === "michi");
  const aiTone = aiInputs.find(node => node.value === "短く");
  assert.equal(aiName.readOnly, true);
  const request = aiDialog.querySelectorAll(".ai-request")[0];
  request.value = "もっと短く";
  button("AIに頼む").fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  const body = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(body.current.name, "michi", "the AI is handed the saved content");
  assert.equal(aiTone.value, "一言だけ", "the proposal fills the form");
  assert.equal(aiName.readOnly, false, "the proposal becomes editable");
});

test("AI edited personal information is saved with the definition without posting", async (t) => {
  const { showPersonaEditor } = await import("../frontend/src/persona-editor.js");
  const persona = { id: "sato", name: "佐藤さん", icon: "🧑", domain: "友人", kind: "person", aliases: [], keywords: [], tools: [] };
  t.mock.method(api, "buildPersona", async () => ({ message: "案です", draft: { ...persona, basicInfo: "佐藤さんは釣りが好き。" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  t.mock.method(api, "post", async () => ({ id: "unexpected" }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });

  showPersonaEditor({ persona, ai: true });
  const walk = node => [node, ...node.children.flatMap(walk)];
  document.body.querySelectorAll(".ai-request")[0].value = "釣りの話を覚えて";
  walk(document.body).find(node => node.textContent === translate("AIに頼む")).fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  const field = document.body.querySelectorAll(".skill-input").find(node => node.value === "佐藤さんは釣りが好き。");
  assert.ok(field);
  assert.equal(field.readOnly, false);
  field.value = "佐藤さんは海釣りが好き。";
  walk(document.body).find(node => node.textContent === translate("保存")).fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  assert.equal(api.savePersonaDraft.mock.calls.length, 1);
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[0].basicInfo, "佐藤さんは海釣りが好き。");
  assert.equal(api.post.mock.calls.length, 0);
});

test("a person's names and keywords are edited as lists and saved with the kind", async (t) => {
  const { showPersonaEditor, splitTerms } = await import("../frontend/src/persona-editor.js");
  assert.deepEqual(splitTerms("釣り、つり, 釣行\n海釣り、 "), ["釣り", "つり", "釣行", "海釣り"]);
  const persona = { id: "sato", name: "佐藤さん", icon: "🧑", domain: "佐藤さんのこと", tone: "", instructions: "", tools: [], kind: "person", aliases: ["サトウ"], keywords: ["釣り"] };
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  globalThis.cancelAnimationFrame = () => {};
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.cancelAnimationFrame; });

  showPersonaEditor({ persona, toolNames: [] });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const inputs = document.body.querySelectorAll(".skill-input");
  const keywords = inputs.find(node => node.value === "釣り");
  keywords.value = "釣り、つり、キャンプ";
  keywords.fire("input");
  walk(document.body).find(node => node.textContent === translate("保存")).fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  const saved = api.savePersonaDraft.mock.calls[0].arguments[0];
  assert.equal(saved.kind, "person");
  assert.deepEqual(saved.aliases, ["サトウ"]);
  assert.deepEqual(saved.keywords, ["釣り", "つり", "キャンプ"]);

  // A person's tone and kinds of note are the app's: hidden, and not saved.
  const field = (root, label) => walk(root).find(node => node.tagName === "LABEL" && node.children[0]?.textContent === translate(label));
  showPersonaEditor({ persona, toolNames: [] });
  const first = document.body.querySelectorAll(".persona-editor").at(-1);
  // Whether a persona is a person does not change, so there is no switch for it.
  assert.ok(!walk(first).some(node => String(node.textContent).startsWith("人物の担当（")));
  assert.equal(field(first, "話し方").hidden, true);
  assert.equal(field(first, "メモの分類（型と、それぞれに書くこと）").hidden, true);
  assert.ok(walk(first).some(node => node.textContent === translate("気を配ること（任意。例：健康の話は控えめに）")));
  walk(first).find(node => node.textContent === translate("閉じる")).fire("click");
  assert.equal(saved.tone, "");
  assert.equal(saved.memory, "");
});


test("a name on the timeline opens what that speaker said, when the list is wired", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const entries = [
    { id: "p", kind: "post", speaker: "user", text: "父に送った", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "father", root: "p", replyTo: "p", text: "覚えました", at: "2026-09-13T00:01:00Z" },
    { id: "s", kind: "post", speaker: "kakeratta", summary: "month", text: "まとめ", at: "2026-09-13T00:02:00Z" },
  ];
  const persona = { id: "father", name: "父", icon: "👴" };
  const opened = [];
  const linked = createFeed({ getPersona: (id) => (id === "father" ? persona : null), showIntro: false, onSpeaker: (id, name) => opened.push([id, name]) });
  linked.set(entries);
  linked.reveal("r"); // The reply is folded until it is opened.
  const names = walk(linked.element).filter(node => String(node.className).split(" ").includes("who"));
  const father = names.find(node => node.textContent === "父");
  const you = names.find(node => node.textContent === translate("あなた"));
  assert.equal(father.tagName, "BUTTON", "a persona's name is a button");
  assert.equal(you.tagName, "BUTTON", "and so is the user's");
  assert.equal(names.find(node => node.textContent === translate("今月のまとめ")).tagName, "SPAN", "the app's summary has no list");
  father.fire("click", { stopPropagation() {} });
  you.fire("click", { stopPropagation() {} });
  assert.deepEqual(opened, [["father", "父"], ["user", translate("あなた")]]);

  // Without the list wired, names are text as before.
  const plain = createFeed({ getPersona: () => persona, showIntro: false });
  plain.set(entries);
  plain.reveal("r");
  assert.ok(walk(plain.element).filter(node => String(node.className).split(" ").includes("who")).every(node => node.tagName === "SPAN"));
});

test("the editor opens at the basic information when asked to", async (t) => {
  const { showPersonaEditor } = await import("../frontend/src/persona-editor.js");
  const persona = { id: "sato", name: "佐藤さん", icon: "🧑", domain: "佐藤さんのこと", tone: "", instructions: "", tools: [], kind: "person", aliases: [], keywords: [], basicInfo: "誕生日は11月10日。" };
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  globalThis.cancelAnimationFrame = () => {};
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.cancelAnimationFrame; });
  const walk = node => [node, ...node.children.flatMap(walk)];

  // Asked for the basic information, it starts there, and scrolls to it.
  TestNode.prototype.scrollIntoView = function () { this.scrolled = true; };
  t.after(() => { delete TestNode.prototype.scrollIntoView; });
  showPersonaEditor({ persona, toolNames: [], focus: "basicInfo" });
  const info = walk(document.body).find(node => node.value === "誕生日は11月10日。");
  assert.ok(info, "the editor shows the saved basic information");
  assert.equal(document.activeElement, info);
  assert.equal(info.scrolled, true);

  // Asked for it for a persona that is not a person, there is no such field
  // to go to: the editor starts at the name as usual.
  document.body.children.length = 0;
  showPersonaEditor({ persona: { ...persona, kind: "", basicInfo: "" }, toolNames: [], focus: "basicInfo" });
  const name = walk(document.body).find(node => node.value === "佐藤さん");
  assert.equal(document.activeElement, name);
});

test("a person on the router's sheet shows the word it was reached by, and can be disputed either way", async (t) => {
  const { showRouting } = await import("../frontend/src/routing-view.js");
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const people = { sato: { id: "sato", name: "佐藤さん", kind: "person" }, mother: { id: "mother", name: "母", kind: "person" } };
  t.mock.method(api, "routing", async () => ({ recorded: true,
    delivered: [{ id: "sato", name: "佐藤さん", person: true, heard: "佐藤", done: true, spoke: false }],
    excluded: [{ id: "mother", name: "母", person: true, reason: "呼び名・キーワードが投稿にありませんでした" }] }));
  const fixes = [];
  await showRouting({ entry: { id: "p", text: "佐藤さんと会った" }, getPersona: (id) => people[id], onFixCondition: (args) => fixes.push(args) });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const nodes = walk(document.body);
  assert.ok(nodes.some(node => node.textContent === translate("「{0}」に一致", "佐藤")));
  nodes.find(node => node.textContent === translate("呼んでほしくなかった")).fire("click");
  assert.equal(fixes[0].persona.id, "sato");
  assert.equal(fixes[0].direction, "exclude");
  nodes.find(node => node.textContent === translate("呼んでほしかった")).fire("click");
  assert.equal(fixes[1].persona.id, "mother");
  assert.equal(fixes[1].direction, "include");
});

test("the router's sheet says a persona was called with @ or by a reply, and has no should-not-have-been-called for it", async (t) => {
  const { showRouting } = await import("../frontend/src/routing-view.js");
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = { gourmet: { id: "gourmet", name: "tabe" }, move: { id: "move", name: "michi" }, book: { id: "book", name: "hon" } };
  t.mock.method(api, "routing", async () => ({ recorded: true,
    delivered: [
      { id: "move", name: "michi", done: true, spoke: true, noted: false },
      { id: "gourmet", name: "tabe", via: "mention", done: true, spoke: true, noted: true, notes: ["shops/soka.md"] },
      { id: "book", name: "hon", via: "reply", done: true, spoke: true, noted: false }],
    excluded: [] }));
  const opened = [];
  await showRouting({ entry: { id: "p", text: "@tabe ラーメン" }, getPersona: (id) => rows[id], onFixCondition: () => {}, onOpenNote: (args) => opened.push(args) });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const texts = walk(document.body).map(node => node.textContent);
  assert.ok(texts.includes(translate("@で呼ばれました")) && texts.includes(translate("返信で呼ばれました")));
  assert.equal(document.body.querySelectorAll(".routing-fix").length, 1, "only the router's own choice can be disputed");
  walk(document.body).find(node => node.textContent === "📝 shops/soka").fire("click");
  assert.equal(opened[0].path, "shops/soka.md", "a called persona's note opens like any other");
});

// The router's call about a post - who it chose, whom it passed over and why -
// is a one-glyph chip on what the user wrote, and opens apart from the
// timeline. Each persona still in the room can be told the router was wrong.
test("the router's call opens from the user's posts and replies and can be disputed", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = [{ id: "gourmet", name: "tabe", icon: "🍜" }, { id: "move", name: "michi", icon: "🚃" }];
  t.mock.method(api, "routing", async (id) => id === "p"
    ? { recorded: true, delivered: [{ id: "gourmet", name: "tabe", icon: "🍜", done: true, spoke: true, noted: true }], excluded: [{ id: "move", name: "michi", icon: "🚃", reason: "移動の話ではない" }, { id: "gone", name: "gone", icon: "", reason: "消えた担当" }] }
    : { recorded: false });
  const disputes = [];
  const feed = createFeed({
    getPersona: (id) => rows.find((row) => row.id === id) || null,
    onFixCondition: (args) => disputes.push(args),
    showIntro: false,
  });
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const texts = () => walk(document.body).map((node) => node.textContent);
  const chips = () => feed.element.querySelectorAll(".routing-open");
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "ラーメン屋まで歩いた", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "gourmet", root: "p", text: "いいですね", at: "2026-09-13T00:01:00Z" },
    { id: "u", kind: "reply", speaker: "user", root: "p", text: "また行く", at: "2026-09-13T00:02:00Z" },
    { id: "q", kind: "post", speaker: "user", pending: true, text: "送信待ち", at: "2026-09-13T00:03:00Z" },
  ]);
  feed.reveal("u");
  assert.equal(chips().length, 2, "the user's post and reply carry the chip; a remark and a pending post do not");
  assert.equal(feed.element.querySelectorAll(".edit-post")[0].textContent, "✎", "editing is a pencil");
  assert.equal(feed.element.querySelectorAll(".routing").length, 0, "nothing about the router sits in the timeline");

  chips()[0].fire("click", {});
  for (let i = 0; i < 3; i++) await new Promise(setImmediate);
  assert.equal(api.routing.mock.calls[0].arguments[0], "p");
  assert.equal(document.body.querySelectorAll(".routing-dialog").length, 1, "the call opens in a dialog of its own");
  assert.equal(document.body.querySelectorAll(".routing-row").length, 3);
  assert.ok(texts().includes("移動の話ではない"), "the router's reason is shown");
  assert.ok(texts().includes(translate("呼ばれました。返事とメモあり")) && texts().includes(translate("呼ばれませんでした")));
  const fixes = document.body.querySelectorAll(".routing-fix");
  assert.equal(fixes.length, 2, "a persona gone from the room cannot be taught");
  fixes[1].fire("click");
  assert.deepEqual(disputes.map((d) => [d.persona.id, d.entry.id, d.direction, d.reason]), [["move", "p", "include", "移動の話ではない"]]);
  assert.equal(document.body.children.length, 0, "disputing closes the dialog so the condition dialog can take its place");

  chips()[1].fire("click", {});
  for (let i = 0; i < 3; i++) await new Promise(setImmediate);
  assert.ok(texts().includes(translate("判断の記録がありません。振り分けの途中なら、終わるとここに出ます。")), "a post the router never answered for says so");
  walk(document.body).find((node) => node.textContent === translate("閉じる")).fire("click");
  assert.equal(document.body.children.length, 0);
});

// What a persona said is where its tone and instructions show, so a remark
// carries the way to dispute them. The user's own lines do not.
test("a persona's remark can be disputed", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = [{ id: "gourmet", name: "tabe", icon: "🍜" }];
  const disputes = [];
  const feed = createFeed({ getPersona: (id) => rows.find((row) => row.id === id) || null, onFixReply: (args) => disputes.push(args), showIntro: false });
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "担々麺", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "gourmet", root: "p", text: "ごま強め", at: "2026-09-13T00:01:00Z" },
    { id: "g", kind: "remark", speaker: "gone", root: "p", text: "消えた担当", at: "2026-09-13T00:02:00Z" },
    { id: "u", kind: "reply", speaker: "user", root: "p", text: "たしかに", at: "2026-09-13T00:03:00Z" },
  ]);
  feed.reveal("u");
  const fixes = feed.element.querySelectorAll(".reply-fix");
  assert.equal(fixes.length, 1, "only a remark by a persona still in the room can be disputed");
  fixes[0].fire("click");
  assert.equal(disputes.length, 1);
  assert.equal(disputes[0].persona.id, "gourmet");
  assert.equal(disputes[0].remark.id, "r");
  assert.equal(disputes[0].post.id, "p");
});

test("an open router's call is refreshed after edits and completion, and ignores late responses", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const pending = [];
  t.mock.method(api, "routing", () => new Promise(resolve => pending.push(resolve)));
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  const post = { id: "p", kind: "post", speaker: "user", text: "old", at: "2026-09-13T00:00:00Z" };
  const record = reason => ({ recorded: true, excluded: [{ id: "book", reason }] });
  const reasons = () => document.body.querySelectorAll(".routing-reason").map(node => node.textContent);
  const settle = async () => { for (let i = 0; i < 3; i++) await new Promise(setImmediate); };
  feed.set([post]);
  await settle();
  assert.equal(pending.length, 0, "nothing is fetched while no sheet is open");
  feed.element.querySelectorAll(".routing-open")[0].fire("click");
  pending.shift()(record("old reason"));
  await settle();
  assert.deepEqual(reasons(), ["old reason"]);

  // A poll that brings an edit refreshes the open sheet.
  feed.set([{ ...post, text: "edited" }]);
  await settle();
  pending.shift()(record("new reason"));
  await settle();
  assert.deepEqual(reasons(), ["new reason"]);

  // The router finishing is announced; a response that arrives after a newer
  // request is ignored.
  const first = feed.refreshRouting("p");
  const late = pending.shift();
  const second = feed.refreshRouting("p");
  pending.shift()(record("latest reason"));
  await second;
  late(record("stale reason"));
  await first;
  await settle();
  assert.deepEqual(reasons(), ["latest reason"]);

  // Another post's completion does not touch this sheet.
  await feed.refreshRouting("other");
  assert.equal(pending.length, 0);

  // Closing the sheet ends the refreshing; a post that disappears closes it.
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  walk(document.body).find((node) => node.textContent === translate("閉じる")).fire("click");
  assert.equal(document.body.children.length, 0);
  await feed.refreshRouting("p");
  assert.equal(pending.length, 0, "a closed sheet is not refreshed");
  feed.element.querySelectorAll(".routing-open")[0].fire("click");
  pending.shift()(record("again"));
  await settle();
  assert.equal(document.body.children.length, 1);
  feed.set([]);
  await settle();
  assert.equal(document.body.children.length, 0, "the sheet closes with its post");
});

// A persona the router called can still have said nothing. The router's call
// says so, and a reply can be asked for from there.
test("a called persona that stayed quiet can be asked for a reply", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = [{ id: "gourmet", name: "tabe", icon: "🍜" }, { id: "wine", name: "ぶどう", icon: "🍷" }, { id: "move", name: "michi", icon: "🚃" }];
  t.mock.method(api, "routing", async () => ({ recorded: true, delivered: [
    { id: "gourmet", name: "tabe", icon: "🍜", done: true, spoke: true, noted: true },
    { id: "wine", name: "ぶどう", icon: "🍷", done: true, noted: true },
    { id: "move", name: "michi", icon: "🚃" },
  ], excluded: [] }));
  const wanted = [];
  const feed = createFeed({ getPersona: (id) => rows.find((row) => row.id === id) || null, onFixReply: (args) => wanted.push(args), showIntro: false });
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "ワインのラベル", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "gourmet", root: "p", text: "白ですね", at: "2026-09-13T00:01:00Z" },
  ]);
  feed.element.querySelectorAll(".routing-open")[0].fire("click", {});
  for (let i = 0; i < 3; i++) await new Promise(setImmediate);
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const texts = walk(document.body).map((node) => node.textContent);
  assert.ok(texts.includes(translate("呼ばれました。返事とメモあり")), "the persona that spoke and noted is simply called");
  assert.ok(texts.includes(translate("呼ばれました。黙ってメモだけ残しました")), "the persona that did not speak is marked");
  assert.ok(texts.includes(translate("呼ばれました。返事を考えています…")), "a persona still working is not called quiet");
  const asks = document.body.querySelectorAll(".reply-wanted");
  assert.equal(asks.length, 1, "only the persona that finished without a word can be asked for a reply");
  asks[0].fire("click");
  assert.equal(wanted.length, 1);
  assert.equal(wanted[0].persona.id, "wine");
  assert.equal(wanted[0].remark, null);
  assert.equal(wanted[0].post.id, "p");
  assert.equal(document.body.children.length, 0, "the sheet gives way to the reply dialog");
});

// A persona whose planner is an external agent is still thinking until the
// agent's turn has really ended; it must not be misread as having stayed quiet.
test("a turn still waiting on the external agent reads as thinking", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = [{ id: "gourmet", name: "tabe", icon: "🍜", planner: "external" }];
  t.mock.method(api, "routing", async () => ({ recorded: true, delivered: [
    { id: "gourmet", name: "tabe", icon: "🍜", done: false },
  ], excluded: [] }));
  const feed = createFeed({ getPersona: (id) => rows.find((row) => row.id === id) || null, showIntro: false });
  feed.set([{ id: "p", kind: "post", speaker: "user", text: "ラーメン", at: "2026-09-13T00:00:00Z" }]);
  feed.element.querySelectorAll(".routing-open")[0].fire("click", {});
  for (let i = 0; i < 3; i++) await new Promise(setImmediate);
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  const texts = () => walk(document.body).map((node) => node.textContent);
  assert.ok(texts().includes(translate("呼ばれました。返事を考えています…")), "still planning, not yet called quiet");
});

// A remark planned by the external agent says so.
test("a remark planned by the external agent is marked", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: (id) => id === "gourmet" ? { id: "gourmet", name: "tabe", icon: "🍜" } : null, showIntro: false });
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "ラーメン", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "gourmet", root: "p", text: "うまそう", model: "agent", at: "2026-09-13T00:01:00Z" },
  ]);
  feed.reveal("r");
  const badges = feed.element.querySelectorAll(".model-badge");
  assert.equal(badges.length, 1);
  assert.equal(badges[0].textContent, translate("外部エージェント"));
});

test("older external replies show escaped newlines as lines", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "一行目\\n二行目", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "gourmet", root: "p", text: "一行目\\n二行目", model: "agent", at: "2026-09-13T00:01:00Z" },
  ]);
  feed.reveal("r");
  const paragraphs = feed.element.querySelectorAll(".said-markdown");
  assert.equal(paragraphs.length, 1);
  const walk = (node) => [node, ...node.children.flatMap(walk)];
  assert.equal(walk(paragraphs[0]).filter((node) => node.tagName === "BR").length, 1);
  assert.equal(feed.element.querySelectorAll(".said").find((node) => node.textContent === "一行目\\n二行目")?.tagName, "P");
});

// A remark says which notes were written with it, and each opens the persona's
// memory at that note.
test("a remark leads to the notes written with it", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const opened = [];
  const feed = createFeed({ getPersona: (id) => id === "gourmet" ? { id: "gourmet", name: "tabe", icon: "🍜" } : null, onOpenNote: (args) => opened.push(args), showIntro: false });
  feed.set([
    { id: "p", kind: "post", speaker: "user", text: "ワインのラベル", at: "2026-09-13T00:00:00Z" },
    { id: "r", kind: "remark", speaker: "gourmet", root: "p", text: "ノートンの白", notes: ["wines/altura.md", "index.md"], at: "2026-09-13T00:01:00Z" },
  ]);
  feed.reveal("r");
  const chips = feed.element.querySelectorAll(".said-note");
  assert.equal(chips.length, 2);
  assert.equal(chips[0].textContent, "📝 wines/altura");
  chips[0].fire("click");
  assert.deepEqual(opened, [{ persona: { id: "gourmet", name: "tabe", icon: "🍜" }, path: "wines/altura.md" }]);
});

test("badges follow the order the viewer saved, and newcomers come after", async () => {
  const { arranged } = await import("../frontend/src/room.js");
  const rows = ["a", "b", "c", "d"].map((id) => ({ id }));
  assert.deepEqual(arranged(rows, ["c", "a"]).map((row) => row.id), ["c", "a", "b", "d"]);
  assert.deepEqual(arranged(rows, ["gone", "d"]).map((row) => row.id), ["d", "a", "b", "c"]);
  assert.deepEqual(arranged(rows, []).map((row) => row.id), ["a", "b", "c", "d"]);
});

test("favorites toggle on posts and standalone persona answers", async t => {
  const state = await import('../frontend/src/favorite-state.js');
  globalThis.Node = TestNode;
  const roots = [];
  globalThis.document = { createElement: tag => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), {textContent:value}), activeElement:null,
    querySelectorAll: () => roots.flatMap(root => root.querySelectorAll('.favorite')) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  t.mock.method(api,'favorites',async () => ({ids:[]}));
  await state.refreshFavorites();
  const calls = [];
  const oldConfirm = globalThis.confirm;
  let confirmations = 0, accepted = false;
  globalThis.confirm = () => { confirmations++; return accepted; };
  t.after(() => { if (oldConfirm) globalThis.confirm = oldConfirm; else delete globalThis.confirm; });
  t.mock.method(api,'setFavorite',async (id, saved) => calls.push([id,saved]));
  const feed = createFeed({getPersona:()=>null, showIntro:false}); roots.push(feed.element);
  feed.set([{id:'favorite-post',kind:'post',speaker:'user',text:'saved',at:'2026-10-02T00:00:00Z'}]);
  const [button] = feed.element.querySelectorAll('.favorite');
  await button.listeners.click[0]();
  assert.equal(button['aria-pressed'],'true');
  await button.listeners.click[0]();
  assert.equal(button['aria-pressed'],'true', 'cancel keeps the favorite');
  assert.equal(calls.length,1);
  accepted = true;
  await button.listeners.click[0]();
  assert.equal(button['aria-pressed'],'false');
  assert.deepEqual(calls,[['favorite-post',true],['favorite-post',false]]);
  assert.equal(confirmations, 2, 'timeline removal always confirms');
  const answer = createFeed({getPersona:()=>null,showIntro:false,standalone:true}); roots.push(answer.element);
  answer.set([{id:'favorite-answer',kind:'remark',speaker:'cafe',root:'old-post',text:'answer',at:'2026-10-02T00:00:00Z'}]);
  assert.equal(answer.element.querySelectorAll('.said-row').length,1);
  const [star] = answer.element.querySelectorAll('.favorite');
  await star.listeners.click[0]();
  assert.equal(star['aria-pressed'],'true');
  await star.listeners.click[0]();
  assert.equal(star['aria-pressed'],'false');
  assert.equal(confirmations, 3, 'persona answers also confirm removal');
  const reply = answer.element.querySelectorAll('.chip').find(node=>node.textContent===translate('返す'));
  assert.equal(reply, undefined, 'favorite previews have no reply link');
});

test("post places show a label without a map link, credits, and coordinate fallback", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: (tag) => new TestNode(tag), createTextNode: value => Object.assign(new TestNode(), { textContent: value }), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const feed = createFeed({ getPersona: () => null, showIntro: false });
  const post = { id: "place", kind: "post", speaker: "user", text: "hello", at: "2026-10-03T00:00:00Z" };
  feed.set([{ ...post, place: { lat: 35, lon: 139, label: "Tokyo（JP）", approximate: true, geonameId: 1850147 } }]);
  const [place] = feed.element.querySelectorAll(".entry-place");
  assert.equal(place.children[0].tagName, "SPAN");
  assert.equal(place.children[0].textContent, "📍 " + translate("{0}付近", "Tokyo（JP）"));
  assert.equal(place.children.length, 1);
  const credits = feed.element.querySelectorAll(".place-credit");
  assert.equal(credits.length, 1);
  assert.equal(credits[0].tagName, "FOOTER");
  assert.equal(credits[0].children[1].href, "https://www.geonames.org/");
  assert.equal(credits[0].children[3].href, "https://creativecommons.org/licenses/by/4.0/");
  feed.set([{ ...post, place: { lat: 0, lon: 0 } }]);
  assert.equal(feed.element.querySelectorAll(".entry-place")[0].children[0].textContent, "📍 0.0000, 0.0000");
  assert.equal(feed.element.querySelectorAll(".place-credit").length, 0);
  feed.set([post]);
  assert.equal(feed.element.querySelectorAll(".entry-place").length, 0);
});


test("profile names and photos appear on user posts and replies alongside persona photos", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: tag => new TestNode(tag), createTextNode: text => Object.assign(new TestNode(), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const photo = "data:image/jpeg;base64,YWJj";
  const settings = { profile: { name: "佐藤", icon: "🌸", avatar: photo } };
  const feed = createFeed({ getSettings: () => settings, getPersona: () => ({ id: "gourmet", name: "tabe", avatar: photo }), showIntro: false });
  feed.set([
    { id: "p-profile", kind: "post", speaker: "user", text: "朝ごはん", at: "2026-10-06T09:00:00Z" },
    { id: "r-profile", kind: "remark", root: "p-profile", replyTo: "p-profile", speaker: "gourmet", text: "いいですね", at: "2026-10-06T09:01:00Z" },
    { id: "u-profile", kind: "reply", root: "p-profile", replyTo: "r-profile", speaker: "user", text: "ありがとう", at: "2026-10-06T09:02:00Z" },
  ]);
  feed.reveal("u-profile");
  assert.equal(feed.element.querySelectorAll(".who").filter(node => node.textContent === "佐藤").length, 2);
  const photos = feed.element.querySelectorAll(".avatar-photo");
  assert.equal(photos.length, 3);
  assert.ok(photos.every(node => node.children[0].src === photo));
  settings.profile.avatar = "";
  feed.draw();
  assert.ok(feed.element.querySelectorAll(".avatar").some(node => node.textContent === "🌸"));
});

test("persona editor keeps the photo field's emoji input mounted when DOM append moves nodes", async t => {
  const { showPersonaEditor } = await import("../frontend/src/persona-editor.js");
  class MovingNode extends TestNode {
    append(...nodes) { for (const node of nodes) { if (node.parent) node.remove(); super.append(node); } }
  }
  globalThis.Node = MovingNode;
  globalThis.document = { createElement: tag => new MovingNode(tag), createTextNode: text => Object.assign(new MovingNode(), { textContent: text }), body: new MovingNode() };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  showPersonaEditor({ persona: { id: "wine", name: "ぶどう", icon: "🍷", domain: "ワイン", tools: [] } });
  const inputs = document.body.querySelectorAll(".avatar-icon-input");
  assert.equal(inputs.length, 1);
  assert.equal(inputs[0].value, "🍷");
  assert.equal(inputs[0].isConnected, true);
});
