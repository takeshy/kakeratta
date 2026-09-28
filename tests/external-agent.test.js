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
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェントが文脈を読んで調べ、回答を書きます。記憶・予定・操作はアプリが決めます。時間内に取得・返却されなければ内蔵モデルが代わりに答えます。")));
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
  // With nothing connected yet, the form to add one is already open, named Hermes.
  const name = walk(view.element).find(n => n["aria-label"] === translate("接続名"));
  assert.equal(name.value, "Hermes");
  await walk(view.element).find(n => n.tag === "button" && n.textContent === translate("追加してキーを発行")).listeners.click();
  assert.deepEqual(api.issueMCPKey.mock.calls[0].arguments[0], { name: "Hermes" });
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

test("connections are a list: each keeps its details, waits and actions, and an issued key stays visible", async t => {
  const { createMCPConnection } = await import("../frontend/src/mcp-connection.js");
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  globalThis.location = { origin: "https://kakeratta.net" };
  globalThis.confirm = () => true;
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; delete globalThis.confirm; });
  let rows = [{ id: "default", name: "Hermes", active: true, claimMinutes: 3, planMinutes: 15 }];
  const payload = () => ({ configured: rows.length > 0, path: "/api/mcp/users/sample", connections: rows.map(row => ({ ...row })) });
  t.mock.method(api, "mcpConnection", async () => payload());
  t.mock.method(api, "issueMCPKey", async body => {
    if (body.id) return { ...payload(), id: body.id, key: `${body.id}-key2` };
    for (const row of rows) row.active = false;
    rows.push({ id: "research", name: body.name, active: true, claimMinutes: 3, planMinutes: 15 });
    return { ...payload(), id: "research", key: "research-key" };
  });
  t.mock.method(api, "selectMCPConnection", async id => {
    for (const row of rows) row.active = row.id === id;
    return payload();
  });
  t.mock.method(api, "setMCPConnectionWaits", async (id, waits) => {
    Object.assign(rows.find(row => row.id === id), waits);
    return payload();
  });
  t.mock.method(api, "revokeMCPKey", async id => {
    rows = rows.filter(row => row.id !== id);
    return payload();
  });
  const view = createMCPConnection(); await view.load();
  const nodes = () => walk(view.element);
  const row = name => nodes().find(n => n.className?.startsWith("mcp-connection-row") && walk(n).some(c => c.textContent === name));
  const within = name => walk(row(name));
  const button = (text, name) => (name ? within(name) : nodes()).find(n => n.tag === "button" && n.textContent === translate(text));
  const radio = name => within(name).find(n => n.type === "radio");
  const header = name => {
    const label = within(name).find(n => n.tag === "dt" && n.textContent === translate("認証ヘッダー"));
    return label.parent.children[label.parent.children.indexOf(label) + 1].children[0].textContent;
  };
  const details = name => within(name).find(n => n.tag === "details" && n.className.includes("mcp-connection-details"));
  // A saved connection shows where to call, but its key only as a placeholder.
  assert.equal(header("Hermes"), "Authorization: Bearer <TOKEN>");
  assert.equal(radio("Hermes").checked, true);

  await button("＋ 接続先を追加").listeners.click();
  const input = nodes().find(n => n["aria-label"] === translate("接続名"));
  input.value = "Research agent"; input.listeners.input();
  await button("追加してキーを発行").listeners.click();
  assert.deepEqual(api.issueMCPKey.mock.calls[0].arguments[0], { name: "Research agent" });
  assert.notEqual(details("Research agent").open, undefined, "the new connection's details open by themselves");
  assert.equal(header("Research agent"), "Authorization: Bearer research-key");
  assert.equal(radio("Research agent").checked, true);

  // Switching keeps the issued key on view for its connection.
  await radio("Hermes").listeners.change();
  assert.deepEqual(api.selectMCPConnection.mock.calls[0].arguments, ["default"]);
  assert.equal(radio("Hermes").checked, true);
  assert.equal(header("Research agent"), "Authorization: Bearer research-key");

  // Waits are the connection's own.
  const claim = within("Research agent").find(n => n.tag === "select" && n["aria-label"] === translate("取得を待つ時間"));
  assert.equal(claim.value, "3");
  claim.value = "10"; await claim.listeners.change();
  assert.deepEqual(api.setMCPConnectionWaits.mock.calls[0].arguments, ["research", { claimMinutes: 10 }]);
  assert.equal(within("Research agent").find(n => n.tag === "select" && n["aria-label"] === translate("取得を待つ時間")).value, "10");
  assert.equal(within("Hermes").find(n => n.tag === "select" && n["aria-label"] === translate("取得を待つ時間")).value, "3");

  await button("キーを再発行", "Research agent").listeners.click();
  assert.deepEqual(api.issueMCPKey.mock.calls[1].arguments[0], { id: "research" });
  assert.equal(header("Research agent"), "Authorization: Bearer research-key2");

  await button("削除", "Research agent").listeners.click();
  assert.deepEqual(api.revokeMCPKey.mock.calls[0].arguments, ["research"]);
  assert.equal(row("Research agent"), undefined);
});

test("a switched-off agent is said to be refused, not merely quiet", async t => {
  const { createMCPConnection } = await import("../frontend/src/mcp-connection.js");
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  globalThis.location = { origin: "https://kakeratta.net" };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; });
  t.mock.method(api, "mcpConnection", async () => ({ configured: true, path: "/api/mcp/users/xxx", connections: [{ id: "h", name: "Hermes", active: true }], agent: { away: true, paused: true, lastSeen: null } }));
  const view = createMCPConnection();
  await view.load();
  const nodes = walk(view.element);
  assert.ok(nodes.some(n => n.textContent === translate("外部エージェントを使わない設定のため、接続を受け付けていません。")));
  assert.ok(!nodes.some(n => n.type === "radio" && n.value === ""), "there is no choosing no connection");
});
