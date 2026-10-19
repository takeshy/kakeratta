import test from "node:test";
import assert from "node:assert/strict";
import { createInbox } from "../internal/server/web/inbox.js";
import { createActions } from "../internal/server/web/actions.js";
import { api } from "../internal/server/web/api.js";
import { createGoogleConnection, finishGoogleConnection } from "../internal/server/web/google.js";
import { createCalendarInbox } from "../internal/server/web/calendar-inbox.js";
import { setupPlan, parseOAuthClient, readOAuthClient, recoveryHint } from "../internal/server/web/setup.js";

class Element {
  children = []; listeners = {}; textContent = ""; value = ""; checked = false;
  constructor(tag) { this.tag = tag; }
  setAttribute(key, value) { this[key] = value; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  remove() { if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  get firstChild() { return this.children[0]; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
}
const walk = node => [node, ...node.children.flatMap(walk)];
function browser(t, hash = "") {
  const old = new Map(["Node", "document", "location", "history", "sessionStorage", "setTimeout"].map(key => [key, globalThis[key]]));
  const storage = new Map(); const navigations = [];
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }), querySelector: () => new Element("toast") };
  globalThis.location = { hash, pathname: "/", search: "", assign: href => navigations.push(href) };
  globalThis.history = { replaceState() { globalThis.location.hash = ""; } };
  globalThis.sessionStorage = { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) };
  globalThis.setTimeout = () => 0;
  t.after(() => { for (const [key, value] of old) { if (value === undefined) delete globalThis[key]; else globalThis[key] = value; } });
  return { storage, navigations };
}
test("Google OAuth removes the callback URL and tab secret before authenticated completion", async t => {
  const { storage } = browser(t, `#google-callback=${encodeURIComponent("state=state&code=code")}`);
  storage.set("google-bridge.oauth.flow", JSON.stringify({ service: "gmail", browserSecret: "tab-secret" }));
  t.mock.method(api, "finishGoogle", async body => {
    assert.equal(location.hash, ""); assert.equal(storage.has("google-bridge.oauth.flow"), false);
    assert.deepEqual(body, { service: "gmail", browserSecret: "tab-secret", state: "state", code: "code" });
    return { connected: true };
  });
  assert.equal(await finishGoogleConnection(), true);
});
test("Google consent is required; client secret never enters browser storage", async t => {
  const { storage, navigations } = browser(t);
  t.mock.method(api, "googleConnection", async () => ({ available: true, redirectUri: "https://app.example/api/google/callback", services: [{ service: "gmail", status: "disconnected", scopes: [] }] }));
  const sent = [];
  t.mock.method(api, "connectGoogle", async body => { sent.push(body); return { url: "https://accounts.google.com/o/oauth2/v2/auth?state=s", browserSecret: "tab-secret" }; });
  const view = createGoogleConnection(); await Promise.resolve();
  const nodes = walk(view.element);
  const form = nodes.find(n => n.tag === "form" && walk(n).some(n => n.textContent === "Googleに接続"));
  const inputs = walk(form).filter(n => n.tag === "input");
  inputs[0].value = "my-project"; inputs[1].value = "client-id"; inputs[2].value = "private-client-secret";
  await form.listeners.submit({ preventDefault() {} }); assert.equal(sent.length, 0);
  inputs.at(-1).checked = true;
  await form.listeners.submit({ preventDefault() {} });
  assert.equal(sent[0].clientSecret, "private-client-secret"); assert.equal(sent[0].modelConsent, true);
  assert.equal(inputs[2].value, ""); assert.equal(navigations.length, 1);
  assert.equal(JSON.stringify([...storage.values()]).includes("private-client-secret"), false);
  view.dispose();
});

const redirectUri = "http://127.0.0.1:8094/api/google/callback";
const clientConfig = () => ({ web: { project_id: "my-project", client_id: "123-abc.apps.googleusercontent.com", client_secret: "private-client-secret", redirect_uris: [redirectUri] } });

test("Setup commands target only selected APIs and reject shell syntax", () => {
  const plan = setupPlan({ projectId: "my-project", services: ["calendar"] });
  assert.ok(plan.commands.includes("gcloud services enable calendar-json.googleapis.com --project=my-project"));
  assert.equal(plan.commands.includes("gmail.googleapis.com"), false);
  assert.equal(plan.commands.includes("projects create"), false);
  assert.equal(plan.commands.includes("config set"), false);
  assert.equal(new URL(plan.links.clients).searchParams.get("project"), "my-project");
  const both = setupPlan({ projectId: "my-project", services: ["gmail", "calendar"], createProject: true });
  assert.ok(both.commands.includes("gcloud projects create my-project"));
  assert.ok(both.commands.includes("gmail.googleapis.com calendar-json.googleapis.com --project=my-project"));
  for (const projectId of ["my-project;whoami", "$(whoami)", "--project", "UPPER", "", "x\nfoo"]) {
    assert.throws(() => setupPlan({ projectId, services: ["gmail"] }));
  }
  assert.throws(() => setupPlan({ projectId: "my-project", services: [] }));
  assert.throws(() => setupPlan({ projectId: "my-project", services: ["gmail;whoami"] }));
});

test("OAuth JSON import checks Web client and exact redirect, never adopts endpoints", async () => {
  const config = clientConfig();
  config.web.token_uri = "https://attacker.invalid/token";
  assert.deepEqual(parseOAuthClient(JSON.stringify(config), redirectUri), { projectId: "my-project", clientId: "123-abc.apps.googleusercontent.com", clientSecret: "private-client-secret" });
  for (const value of [null, {}, { installed: config.web }, { web: { ...config.web, client_id: "attacker.invalid" } }, { web: { ...config.web, redirect_uris: [redirectUri + "/"] } }, { web: { ...config.web, client_secret: "secret\nvalue" } }]) {
    assert.throws(() => parseOAuthClient(JSON.stringify(value), redirectUri), /JSON|URI/);
  }
  assert.throws(() => parseOAuthClient('{"secret":"PRIVATE', redirectUri), error => !error.message.includes("PRIVATE"));
  let read = false;
  await assert.rejects(readOAuthClient({ size: 65537, text() { read = true; } }, redirectUri), /64KiB/);
  assert.equal(read, false);
});

test("Import stays in password fields until explicit consent; disposal clears it", async t => {
  const { storage } = browser(t);
  t.mock.method(api, "googleConnection", async () => ({ available: true, redirectUri, services: [{ service: "gmail", status: "disconnected", scopes: [] }] }));
  t.mock.method(api, "connectGoogle", async () => assert.fail("JSON import cannot initiate OAuth"));
  const widget = createGoogleConnection(); await Promise.resolve();
  const nodes = walk(widget.element);
  const file = nodes.find(n => n.tag === "input" && n.type === "file");
  file.files = [{ size: 300, text: async () => JSON.stringify(clientConfig()) }];
  await file.listeners.change();
  assert.equal(nodes.find(n => n.tag === "input" && n.type === "password").value, "private-client-secret");
  assert.equal(file.value, "");
  assert.equal(storage.size, 0);
  assert.equal(nodes.some(n => n.textContent.includes("private-client-secret")), false);
  widget.dispose();
  assert.equal(widget.element.children.length, 0);
});

test("Recovery guidance handles fixed error categories without echoing provider data", () => {
  assert.match(recoveryHint("redirect_uri_mismatch"), /完全一致/);
  assert.match(recoveryHint("Google authorization expired or was revoked; reconnect"), /同じタブ/);
  assert.equal(recoveryHint("unknown PRIVATE secret" ).includes("PRIVATE"), false);
});

test("Calendar rules show the editable prompt and series IDs; removal requires save", async t => {
  browser(t);
  let saved;
  const settings = { enabled: true, llmEnabled: true, calendarId: "primary", timeZone: "Asia/Tokyo", bodyChars: 2000, triagePrompt: "移動の必要な予定を候補に", excludedEvents: [{ calendarId: "primary", eventId: "instance", seriesId: "master", label: "毎日の定例" }] };
  t.mock.method(api, "calendarInbox", async () => ({ settings, events: [], triagePending: 0 }));
  t.mock.method(api, "saveCalendarInboxSettings", async body => { saved = body; });
  const widget = createCalendarInbox(); await Promise.resolve();
  const nodes = walk(widget.element);
  assert.ok(nodes.some(n => n.textContent === "primary / master"));
  const prompt = nodes.find(n => n.tag === "textarea"); prompt.value = "予約と移動を優先";
  nodes.find(n => n.textContent === "この除外を解除").listeners.click();
  assert.equal(saved, undefined);
  const form = nodes.find(n => n.tag === "form");
  await form.listeners.submit({ preventDefault() {} });
  assert.deepEqual(saved.excludedEvents, []);
  assert.equal(saved.triagePrompt, "予約と移動を優先");
  widget.dispose(); assert.equal(widget.element.children.length, 0);
});
test("Google denial clears flow state without posting a code", async t => {
  const { storage } = browser(t, `#google-callback=${encodeURIComponent("error=access_denied&state=s")}`);
  storage.set("google-bridge.oauth.flow", JSON.stringify({ service: "gmail", browserSecret: "tab-secret" }));
  t.mock.method(api, "finishGoogle", async () => assert.fail("denied flow must not exchange a code"));
  assert.equal(await finishGoogleConnection(), true); assert.equal(storage.size, 0); assert.equal(location.hash, "");
});

test("Review shows exact content as text and only sends the stored hash to approve", async t => {
  browser(t);
  const action = { id: "proposal-id", type: "gmail_send", payloadHash: "sealed-hash", status: "pending", createdAt: "2026-10-19T10:00:00Z", expiresAt: "2026-10-19T10:05:00Z", payload: { from: "me@example.com", mail: { to: ["real@example.com"], subject: "<script>external</script>", body: "private body" } } };
  t.mock.method(api, "googleActions", async () => ({ actions: [action] }));
  const decisions = []; const executions = [];
  t.mock.method(api, "decideGoogleAction", async (...args) => { decisions.push(args); action.status = "approved"; });
  t.mock.method(api, "executeGoogleAction", async (...args) => { executions.push(args); action.status = "succeeded"; action.payload = null; });
  const widget = createActions(); await Promise.resolve();
  let nodes = walk(widget.element);
  assert.ok(nodes.some(n => n.textContent === "<script>external</script>"));
  assert.ok(nodes.some(n => n.textContent === "real@example.com"));
  assert.ok(nodes.some(n => n.textContent === "private body"));
  assert.equal(nodes.some(n => n.textContent === "承認済みの内容を実行"), false);
  let button = nodes.find(n => n.textContent === "この内容を承認");
  await button.listeners.click({ currentTarget: button });
  assert.deepEqual(decisions, [["proposal-id", "sealed-hash", true]]);
  assert.equal(executions.length, 0);
  nodes = walk(widget.element); button = nodes.find(n => n.textContent === "承認済みの内容を実行");
  await button.listeners.click({ currentTarget: button });
  assert.deepEqual(executions, [["proposal-id", "sealed-hash"]]);
  assert.equal(walk(widget.element).some(n => n.textContent === "private body"), false);
  widget.dispose(); assert.equal(widget.element.children.length, 0);
});

test("Inbox is explicit opt-in, shows external text and does not prefill the Timeline secret", async t => {
  browser(t);
  let removed = 0;
  t.mock.method(api, "timelineTarget", async () => ({ configured: true, enabled: true, url: "https://example.com/api/mcp/users/owner" }));
  t.mock.method(api, "googleInbox", async () => ({ settings: { enabled: false, threshold: 50, includeLabels: ["INBOX"], priorityLabels: ["IMPORTANT"], prioritySenders: [], excludedSenders: [], bodyChars: 2000, retentionDays: 7, lookbackDays: 1 }, messages: [{ messageId: "m1", connectionId: "c1", threadId: "t1", subject: "<script>untrusted</script>", from: "sender@example.com", body: "external body", sourceUrl: "https://mail.google.com/mail/u/0/#all/t1", receivedAt: "2026-10-19T10:00:00Z", importedAt: "2026-10-19T10:01:00Z", classification: { importance: 80, urgency: 20, reason: "selected label" } }] }));
  t.mock.method(api, "clearInbox", async () => { removed++; });
  const widget = createInbox(); await Promise.resolve(); await Promise.resolve();
  const nodes = walk(widget.element);
  assert.ok(nodes.some(n => n.textContent === "<script>untrusted</script>"));
  assert.equal(nodes.find(n => n.tag === "input" && n.type === "password").value, "");
  assert.equal(nodes.find(n => n.textContent === "今すぐ差分を取得").disabled, true);
  const remove = nodes.find(n => n.textContent === "保存データを削除して停止");
  await remove.listeners.click({ currentTarget: remove }); assert.equal(removed, 0);
  const confirm = nodes.filter(n => n.tag === "input" && n.type === "checkbox").at(-1); confirm.checked = true;
  await remove.listeners.click({ currentTarget: remove }); assert.equal(removed, 1);
  widget.dispose(); assert.equal(widget.element.children.length, 0);
});
