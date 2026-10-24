import test from "node:test";
import assert from "node:assert/strict";
import { agentMention, agentMentions, createMentionPicker, mentionQuery, mentionTag, replaceMention } from "../frontend/src/mention-picker.js";

import { getLanguage, setLanguage } from "../frontend/src/i18n.js";

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

test("mention ranges handle Japanese, full width @, caret insertion and email", () => {
  assert.deepEqual(mentionQuery("相談。＠ワイ", 6), { start: 3, end: 6, query: "ワイ" });
  assert.equal(mentionQuery("me@example.com", 14), null);
  assert.equal(mentionQuery("@wine", 5, 3), null);
  assert.equal(mentionTag({ name: "ワイン係", id: "wine" }), "@ワイン係");
  assert.equal(mentionTag({ name: "Wine Expert", id: "wine" }), "@wine");
  assert.equal(mentionTag({ name: "ワインさん", id: "wine" }), "@wine");
  assert.equal(replaceMention("相談 @tabe 続き", "@michi"), "相談 @michi 続き");
  assert.equal(replaceMention("相談", "@michi"), "相談 @michi ");
});

test("picker does not offer a second mention", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const input = new TestNode();
  const picker = createMentionPicker({ input, getPersonas: () => [
    { id: "wine", name: "ワイン係", active: true },
  ] });
  input.value = "@book 質問 @";
  input.setSelectionRange(input.value.length, input.value.length);
  input.fire("input");
  assert.equal(picker.element.hidden, true);
});

test("picker selects active personas, preserves surrounding text, and respects IME", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const input = new TestNode();
  let changes = 0;
  const picker = createMentionPicker({ input, onChange: () => changes++, getPersonas: () => [
    { id: "wine", name: "ワイン係", active: true },
    { id: "book", name: "読書係", active: true },
    { id: "off", name: "休み", active: false },
  ] });
  input.value = "相談 @ 後半";
  input.setSelectionRange(4, 4);
  input.fire("input");
  assert.equal(picker.element.children.length, 2);
  input.fire("keydown", { key: "Enter", isComposing: true });
  assert.equal(changes, 0);
  input.fire("keydown", { key: "ArrowDown", preventDefault() {} });
  input.fire("keydown", { key: "Enter", preventDefault() {} });
  assert.equal(input.value, "相談 @読書係  後半");
  assert.equal(changes, 1);
  assert.equal(picker.element.hidden, true);
  input.value = "＠wine";
  input.setSelectionRange(5, 5);
  input.fire("input");
  assert.equal(picker.element.children.length, 1);
  picker.element.children[0].fire("click");
  assert.equal(input.value, "@ワイン係 ");
});

test("picker offers the external agent by a reserved name", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const agent = agentMention(true);
  assert.equal(mentionTag(agent), "@agent");
  const input = new TestNode();
  const picker = createMentionPicker({ input, getPersonas: () => [{ id: "wine", name: "ワイン係", active: true }, agent] });
  input.value = "@age";
  input.setSelectionRange(4, 4);
  input.fire("input");
  assert.equal(picker.element.children.length, 1);
  picker.element.children[0].fire("click");
  assert.equal(input.value, "@agent ");
});

test("agent destination labels and explicit addresses are localized", t => {
  const previous = getLanguage();
  t.after(() => setLanguage(previous));
  for (const [language, builtin, external] of [["ja", "内蔵AI", "外部エージェント"], ["en", "Built-in AI", "External agent"]]) {
    setLanguage(language);
    assert.equal(agentMention(false).name, builtin);
    assert.equal(agentMention(true).name, external);
    assert.equal(mentionTag(agentMention(false)), "@ai");
    assert.equal(mentionTag(agentMention(true)), "@agent");
  }
});


test("each address offers only its destination and disabled external is hidden", t => {
  globalThis.Node = TestNode;
  globalThis.document = { createElement: () => new TestNode(), activeElement: null };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const input = new TestNode();
  let externalAvailable = true;
  const picker = createMentionPicker({ input, getPersonas: () => agentMentions(externalAvailable) });
  const type = text => {
    input.value = text;
    input.setSelectionRange(text.length, text.length);
    input.fire("input");
  };
  type("@");
  assert.equal(picker.element.children.length, 2);
  type("@agent");
  assert.equal(picker.element.children.length, 1);
  picker.element.children[0].fire("click");
  assert.equal(input.value, "@agent ");
  type("@ai");
  assert.equal(picker.element.children.length, 1);
  picker.element.children[0].fire("click");
  assert.equal(input.value, "@ai ");
  externalAvailable = false;
  type("@agent");
  assert.equal(picker.element.hidden, true);
  type("@");
  assert.equal(picker.element.children.length, 1);
  picker.element.children[0].fire("click");
  assert.equal(input.value, "@ai ");
});
