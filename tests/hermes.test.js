import test from "node:test";
import assert from "node:assert/strict";
import { createPersonas } from "../frontend/src/personas.js";
import { api } from "../frontend/src/api.js";
import { t as translate } from "../frontend/src/i18n.js";

class Element {
  children = []; listeners = {}; textContent = ""; className = "";
  constructor(tag) { this.tag = tag; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  get firstChild() { return this.children[0]; }
  setAttribute(key, value) { this[key] = key === "disabled" || key === "checked" ? true : value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
}
const walk = node => [node, ...node.children.flatMap(walk)];

test("Hermes card exposes shared notes without a redundant request button", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const row = { id: "hermes", name: "Hermes", icon: "🤖", builtin: true, active: true, tools: [] };
  t.mock.method(api, "personas", async () => ({ personas: [row], tools: [] }));
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  const nodes = walk(view.element);
  assert.ok(!nodes.some(n => n.tag === "button" && n.textContent === "Hermesに聞く"));
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェント")));
  assert.ok(nodes.some(n => n.textContent === translate("覚えていること")));
});

test("Hermes setup includes the key in one configuration and links to its manual", async t => {
  const { createMCPConnection } = await import("../frontend/src/mcp-connection.js");
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  globalThis.location = { origin: "https://kakeratta.net" };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; });
  const key = "a".repeat(64);
  t.mock.method(api, "mcpConnection", async () => ({ configured: false, path: "/api/mcp/users/xxx" }));
  t.mock.method(api, "issueMCPKey", async () => ({ configured: true, path: "/api/mcp/users/xxx", key }));
  const view = createMCPConnection();
  await view.load();
  await walk(view.element).find(n => n.tag === "button" && n.textContent === translate("接続キーを発行")).listeners.click();
  const fields = walk(view.element).filter(n => n.tag === "textarea");
  // The configuration comes in three shapes now: a prompt for a plain Hermes,
  // a NemoClaw command, and the manual YAML.
  assert.equal(fields.length, 3);
  const config = fields.find(n => n["aria-label"] === translate("HermesのMCP接続設定"));
  assert.ok(config, "the manual YAML is there");
  assert.ok(config.value.includes(`Authorization: "Bearer ${key}"`));
  assert.ok(!config.value.includes("KAKERATTA_MCP_TOKEN"));
  assert.match(config.value, /https:\/\/kakeratta.net\/api\/mcp\/users\/xxx/);
  const help = walk(view.element).find(n => n.tag === "a" && n.href?.includes("-hermes"));
  assert.ok(help);
  assert.equal(help.target, "_blank");
  assert.equal(help.rel, "noopener");
});
