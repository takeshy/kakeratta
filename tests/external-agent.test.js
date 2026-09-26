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

test("the planner choice stays hidden until an external agent is connected", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = [
    { id: "gourmet", name: "tabe", icon: "🍜", builtin: true, active: true, tools: [], planner: "builtin" },
    { id: "book", name: "shiori", icon: "📖", builtin: true, active: true, tools: [], planner: "external" },
  ];
  t.mock.method(api, "personas", async () => ({ personas: rows, tools: [], mcpConfigured: false }));
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  const selects = walk(view.element).filter(n => n.tag === "select" && n["aria-label"] === translate("考える役"));
  // Only the persona already handed over shows it, so it can be handed back.
  assert.equal(selects.length, 1);
  assert.equal(selects[0].value, "external");
});

test("each persona chooses its planner, and Hermes is not a persona", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let rows = [{ id: "gourmet", name: "tabe", icon: "🍜", builtin: true, active: true, tools: [], planner: "builtin" }];
  t.mock.method(api, "personas", async () => ({ personas: rows, tools: [], mcpConfigured: true }));
  const chosen = [];
  t.mock.method(api, "setPlanner", async (id, planner) => {
    chosen.push([id, planner]);
    rows = [{ ...rows[0], planner }];
    return { personas: rows, mcpConfigured: true };
  });
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  let nodes = walk(view.element);
  assert.ok(!nodes.some(n => n.textContent === "Hermes"));
  assert.ok(!nodes.some(n => n.textContent === translate("外部エージェント")), "no badge while planned in-process");
  const select = nodes.find(n => n.tag === "select" && n["aria-label"] === translate("考える役"));
  assert.ok(select);
  select.value = "external";
  await select.listeners.change();
  assert.deepEqual(chosen, [["gourmet", "external"]]);
  nodes = walk(view.element);
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェント")));
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェントが文脈を読み、次にすることを workflow にします。時間内に取得・返却されなければ内蔵モデルが代わりに考えます。")));
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
