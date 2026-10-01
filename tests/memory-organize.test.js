import test from "node:test";
import assert from "node:assert/strict";
import { createMemoryOrganization } from "../frontend/src/memory-organize.js";
import { setLanguage } from "../frontend/src/i18n.js";

class Element {
  children = []; attributes = {}; events = {}; textContent = ""; isConnected = true;
  constructor(tag) { this.tagName = tag.toUpperCase(); }
  setAttribute(key, value) { this.attributes[key] = value; if (key === "hidden") this.hidden = true; }
  addEventListener(key, cb) { this.events[key] = cb; }
  append(...nodes) { this.children.push(...nodes); }
  replaceChildren(...nodes) { this.children = nodes; }
  all() { return [this, ...this.children.flatMap(node => node.all())]; }
  focus() {}
}
function dom(t) {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  setLanguage("ja");
  t.after(() => { delete globalThis.document; delete globalThis.Node; });
}

test("research displays policy and exact changes; only OK executes", async t => {
  dom(t);
  const calls = [];
  const host = createMemoryOrganization({ id: "gourmet", externalAvailable: true, api: {
    organizeMemory: async (...args) => { calls.push(["research", ...args]); return { token: "saved", status: "ready", policy: "重複を統合する", changes: [{ path: "merged.md", text: "<script>untrusted</script>" }, { path: "old.md", delete: true }] }; },
    applyMemoryOrganization: async (...args) => calls.push(["apply", ...args]),
  }, onApplied: () => calls.push(["refresh"]) });
  const nodes = host.all();
  const request = nodes.find(node => node.tagName === "TEXTAREA"); request.value = "まとめて";
  nodes.find(node => node.tagName === "SELECT").value = "external";
  const research = nodes.find(node => node.textContent === "調べて整理方針を出す");
  const approve = nodes.find(node => node.textContent === "OK、この方針で実行");
  assert.equal(approve.hidden, true);
  await research.events.click();
  assert.deepEqual(calls, [["research", "gourmet", "まとめて", "external"]]);
  assert.ok(host.all().some(node => node.textContent === "重複を統合する"));
  assert.ok(host.all().some(node => node.tagName === "PRE" && node.textContent === "<script>untrusted</script>"));
  assert.equal(host.all().some(node => node.tagName === "SCRIPT"), false);
  assert.equal(approve.hidden, false);
  await approve.events.click();
  assert.deepEqual(calls.slice(1), [["apply", "gourmet", "saved"], ["refresh"]]);
});

test("editing the request invalidates its approval; unconnected agents cannot be selected", async t => {
  dom(t);
  let applied = 0;
  const host = createMemoryOrganization({ id: "gourmet", externalAvailable: false, api: {
    organizeMemory: async () => ({ token: "saved", status: "ready", policy: "整理", changes: [{ path: "a.md", text: "changed" }] }),
    applyMemoryOrganization: async () => applied++,
  } });
  const request = host.all().find(node => node.tagName === "TEXTAREA"); request.value = "整理";
  await host.all().find(node => node.textContent === "調べて整理方針を出す").events.click();
  request.value = "消さないで"; request.events.input();
  const approve = host.all().find(node => node.textContent === "OK、この方針で実行");
  assert.equal(approve.hidden, true);
  await approve.events.click();
  assert.equal(applied, 0);
  assert.equal(host.all().some(node => node.tagName === "OPTION" && node.attributes.value === "external"), false);
});
