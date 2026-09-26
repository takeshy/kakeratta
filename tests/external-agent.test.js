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
// The "prefer the external agent" checkbox of each persona card.
const preferBoxes = root => walk(root).filter(n => n.tag === "label" && n.children.some(c => c.textContent === translate("外部エージェントを優先する")))
  .map(label => label.children.find(c => c.tag === "input"));

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
  const boxes = preferBoxes(view.element);
  // Only the persona already handed over shows it, so it can be handed back.
  assert.equal(boxes.length, 1);
  assert.equal(boxes[0].checked, true);
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
  const [prefer] = preferBoxes(view.element);
  assert.ok(prefer);
  assert.ok(!prefer.checked);
  prefer.checked = true;
  await prefer.listeners.change();
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

test("a paused external agent is said so on the persona it plans for", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  const rows = [{ id: "book", name: "shiori", icon: "📖", builtin: true, active: true, tools: [], planner: "external" }];
  t.mock.method(api, "personas", async () => ({ personas: rows, tools: [], mcpConfigured: true, externalAgentPaused: true }));
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  const nodes = walk(view.element);
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェントは一時停止中です。再開するまで内蔵モデルが考えます。")));
  assert.equal(preferBoxes(view.element)[0].checked, true, "the setting is kept");
});

test("the check-in has no time to set; the checkbox chooses how much it says", async t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  let rows = [{ id: "gourmet", name: "tabe", icon: "🍜", builtin: true, active: true, tools: [], dailyCheckIn: { enabled: true, time: "09:00", timeZone: "Asia/Tokyo", talk: "minimal" } }];
  t.mock.method(api, "personas", async () => ({ personas: rows, tools: [] }));
  const saved = [];
  t.mock.method(api, "setDailyCheckIn", async (id, body) => {
    saved.push(body);
    rows = [{ ...rows[0], dailyCheckIn: body }];
    return { personas: rows };
  });
  const view = createPersonas({ getSettings: () => ({}) });
  await view.load();
  const label = walk(view.element).find(n => n.tag === "label" && n.children.some(c => c.textContent === translate("チェックインでは最低限のことだけ話す")));
  const minimal = label.children.find(c => c.tag === "input");
  assert.equal(minimal.checked, true);
  minimal.checked = false;
  await minimal.listeners.change();
  assert.equal(saved[0].talk, "share");
  assert.deepEqual(saved[0], { talk: "share" }, "only how much it says is sent; when is the app's");
});

test("the connection says whether the agent is calling in", async t => {
  const { createMCPConnection } = await import("../frontend/src/mcp-connection.js");
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  globalThis.location = { origin: "https://kakeratta.net" };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; });
  t.mock.method(api, "mcpConnection", async () => ({ configured: true, path: "/api/mcp/users/xxx", agent: { away: true, lastSeen: null } }));
  const view = createMCPConnection();
  await view.load();
  assert.ok(walk(view.element).some(n => n.textContent === translate("外部エージェントからまだ接続がありません。接続があるまで内蔵モデルが考えます。")));
});
