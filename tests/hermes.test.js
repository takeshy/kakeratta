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

test("Hermes card opens a direct request and exposes shared notes", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const row = { id: "hermes", name: "Hermes", icon: "🤖", model: "high", builtin: true, active: true, tools: [] };
  t.mock.method(api, "personas", async () => ({ personas: [row], tools: [] }));
  let asked;
  const view = createPersonas({ getSettings: () => ({}), onAsk: value => { asked = value; } });
  await view.load();
  const nodes = walk(view.element);
  const button = nodes.find(n => n.tag === "button" && n.textContent === translate("Hermesに聞く"));
  assert.ok(button);
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェント")));
  assert.ok(nodes.some(n => n.textContent === translate("覚えていること")));
  button.listeners.click();
  assert.equal(asked, row);
  row.active = false;
  await view.load();
  assert.equal(walk(view.element).find(n => n.textContent === translate("Hermesに聞く")).disabled, true);
});

test("MCP setup separates the token from config.yaml", async t => {
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
  await walk(view.element).find(n => n.tag === "button" && n.textContent === translate("MCPキーを発行")).listeners.click();
  const fields = walk(view.element).filter(n => n.tag === "textarea");
  assert.equal(fields.length, 2);
  assert.match(fields[0].value, /Authorization: "Bearer \$\{KAKERATTA_MCP_TOKEN\}"/);
  assert.ok(!fields[0].value.includes(key));
  assert.match(fields[0].value, /https:\/\/kakeratta.net\/api\/mcp\/users\/xxx/);
  assert.equal(fields[1].value, `KAKERATTA_MCP_TOKEN=${key}\n`);
});
