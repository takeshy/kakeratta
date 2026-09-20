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

test("search reveals a folded reply and polling preserves a later manual fold", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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

// The person watching a folded timeline cannot tell that a persona has just
// spoken. A remark that arrives after the timeline is showing unfolds its
// post; the first load and older pages do not, and a fold closed by hand stays
// closed until the next remark.
test("a persona's remark arriving unfolds its post; loads and older pages do not", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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

test("the user's own reply can be deleted", async (t) => {
  const post = { id: "p", kind: "post", speaker: "user", root: "p", text: "root", at: "2026-09-13T00:00:00Z" };
  const reply = { id: "u", kind: "reply", speaker: "user", root: "p", replyTo: "p", text: "my reply", at: "2026-09-13T00:01:00Z" };
  t.mock.method(api, "remove", async () => ({ ...reply, deleted: true }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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

test("persona builder dictates with saved settings, sends, and stops on close", async (t) => {
  const { showPersonaBuilder } = await import("../frontend/src/persona-builder.js");
  t.mock.method(api, "buildPersona", async () => ({ message: "続けて教えてください", draft: null }));
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), body: new TestNode(), activeElement: null };
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
  const input = document.body.querySelectorAll(".skill-input")[0];
  button("🎙 話す").fire("click");
  assert.equal(recognizers[0].lang, "ja-JP");
  recognizers[0].result("ワインの担当を作りたい");
  assert.equal(input.value, "ワインの担当を作りたい");
  recognizers[0].result("ワインの担当を作りたい送信");
  await new Promise(setImmediate);
  assert.equal(api.buildPersona.mock.calls[0].arguments[0].messages[0].content, "ワインの担当を作りたい");
  assert.equal(recognizers[0].stopped, true);
  button("🎙 話す").fire("click");
  button("閉じる").fire("click");
  assert.equal(recognizers[1].stopped, true);
  assert.equal(document.body.children.length, 0);
});


test("read aloud selects only one thread, including user replies, and updates its stop button", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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

test("persona bar groups overflow and toggles without losing the selected persona", async t => {
  const { createRoom } = await import("../frontend/src/room.js");
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let selected;
  const room = createRoom({ onSelect: row => { selected = row.id; } });
  room.set(Array.from({ length: 6 }, (_, i) => ({ id: String(i), name: `担当${i}`, active: true })));
  const more = room.bar.querySelectorAll(".persona-more")[0];
  assert.equal(more.textContent, translate("その他{0}体", 4));
  assert.equal(room.bar.querySelectorAll(".persona-overflow").length, 4);
  more.fire("click");
  assert.equal(room.bar.classList.contains("expanded"), true);
  room.bar.querySelectorAll(".persona-overflow")[0].fire("click");
  assert.equal(selected, "2");
  more.fire("click");
  assert.equal(room.bar.classList.contains("expanded"), false);
  room.set([{ id: "one", name: "担当", active: true }]);
  assert.equal(room.bar.querySelectorAll(".persona-more").length, 0);
});

test("daily check-ins display their persona and sources as replyable parent posts", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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

test("persona editor edits directly, asks the AI underneath, and shows what changed", async (t) => {
  const { showPersonaEditor } = await import("../frontend/src/persona-editor.js");
  const persona = { id: "move", name: "michi", icon: "🚃", domain: "移動", tone: "短く", instructions: "場所を覚える", model: "middle", tools: ["route", "place_search"] };
  t.mock.method(api, "buildPersona", async (body) => ({ message: "短くしました", draft: { ...body.current, tone: "一言だけ" } }));
  t.mock.method(api, "savePersonaDraft", async (skill) => skill);
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
  globalThis.cancelAnimationFrame = () => {};
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.cancelAnimationFrame; });

  let saved = false;
  showPersonaEditor({ persona, toolNames: ["web_search", "place_search", "route", "schedule"], onSave: () => { saved = true; } });
  const walk = node => [node, ...node.children.flatMap(walk)];
  const button = label => walk(document.body).find(node => node.textContent === translate(label));
  const inputs = document.body.querySelectorAll(".skill-input");
  const name = inputs.find(node => node.value === "michi");
  const tone = inputs.find(node => node.value === "短く");

  // The original is in the form, editable directly.
  name.value = "みち";
  name.fire("input");
  const previous = document.body.querySelectorAll(".previous");
  assert.ok(previous.some(node => !node.hidden && walk(node).some(child => child.textContent === "michi")), "the original name is shown next to the edit");

  // The AI box sits open underneath and sends the form as it stands.
  const request = document.body.querySelectorAll(".ai-request")[0];
  assert.ok(!request.parent.hidden);
  request.value = "もっと短く";
  button("AIに頼む").fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  const body = api.buildPersona.mock.calls[0].arguments[0];
  assert.equal(body.current.name, "みち", "the AI is handed the unsaved edit");
  assert.equal(tone.value, "一言だけ", "the proposal fills the form");
  assert.ok(previous.some(node => !node.hidden && walk(node).some(child => child.textContent === "短く")), "the replaced tone is shown");

  button("保存").fire("click");
  for (let i = 0; i < 5; i++) await new Promise(setImmediate);
  const skill = api.savePersonaDraft.mock.calls[0].arguments[0];
  assert.equal(skill.name, "みち");
  assert.equal(skill.tone, "一言だけ");
  assert.deepEqual(skill.tools, ["place_search", "route"]);
  assert.equal(api.savePersonaDraft.mock.calls[0].arguments[1], "move");
  assert.ok(saved);
});

// The router's call about a post - who it chose, whom it passed over and why -
// is a one-glyph chip on what the user wrote, and opens apart from the
// timeline. Each persona still in the room can be told the router was wrong.
test("the router's call opens from the user's posts and replies and can be disputed", async (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
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
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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
  globalThis.document = { createElement: () => new TestNode(), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
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
  globalThis.document = { createElement: () => new TestNode(), body: new TestNode(), activeElement: null, querySelector: () => new TestNode() };
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


// A remark says which notes were written with it, and each opens the persona's
// memory at that note.
test("a remark leads to the notes written with it", (t) => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
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
