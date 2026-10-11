import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { startAlexaLink, showAlexaLink, createAlexaConnection } from "../frontend/src/alexa.js";
import { setLanguage } from "../frontend/src/i18n.js";

class Element {
  children = []; listeners = {}; textContent = "";
  constructor(tag) { this.tag = tag; }
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  get firstChild() { return this.children[0]; }
  remove() { this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  setAttribute(key, value) { this[key] = value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
}
const walk = node => [node, ...node.children.flatMap(walk)];
function dom(t) {
  setLanguage("ja");
  const root = new Element("root");
  globalThis.Node = Element;
  globalThis.document = { querySelector: () => root, createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; delete globalThis.location; setLanguage("ja"); });
  return root;
}

test("Alexa linking waits for consent, preserves OAuth state, and only follows the server redirect", async t => {
  const root = dom(t), requests = [];
  let redirected;
  globalThis.location = { search: "?client_id=client&redirect_uri=https%3A%2F%2Fexample.com%2Fcallback&response_type=code&scope=alexa&state=a%26b", replace: value => { redirected = value; } };
  t.mock.method(globalThis, "fetch", async (path, options) => {
    requests.push({ path, options });
    return new Response(JSON.stringify({ redirect: "https://example.com/callback?code=server-code" }));
  });
  showAlexaLink();
  assert.equal(requests.length, 0);
  const button = walk(root).find(node => node.tag === "button");
  await button.listeners.click();
  assert.equal(requests[0].path, "/api/alexa/authorize");
  assert.equal(requests[0].options.method, "POST");
  assert.equal(JSON.parse(requests[0].options.body).state, "a&b");
  assert.equal(redirected, "https://example.com/callback?code=server-code");
});

test("Alexa consent waits for session restoration and preserves the OAuth request", async t => {
  const root = dom(t);
  const search = "?client_id=client&redirect_uri=https%3A%2F%2Fexample.com%2Fcallback&response_type=code&scope=alexa&state=a%26b";
  globalThis.location = { pathname: "/alexa", search, replace() { assert.fail("session restoration must preserve the linking URL"); } };
  let restore;
  const pending = startAlexaLink({ reconnect: () => new Promise(resolve => { restore = resolve; }) });
  assert.equal(walk(root).some(node => node.tag === "button"), false, "consent is unavailable while restoring the session");
  restore();
  await pending;
  assert.equal(location.search, search);
  const button = walk(root).find(node => node.tag === "button");
  assert.equal(button.textContent, "Alexaと連携する");
  t.mock.method(globalThis, "fetch", async (_path, options) => {
    const body = JSON.parse(options.body);
    for (const [key, value] of new URLSearchParams(search)) assert.equal(body[key], value);
    return new Response(JSON.stringify({ error: "keep consent visible" }), { status: 503 });
  });
  await button.listeners.click();
});

test("Alexa startup preserves the login screen when session recovery requires sign-in", async t => {
  const root = dom(t);
  globalThis.location = { search: "?state=keep-me" };
  const login = new Element("login");
  await startAlexaLink({ reconnect: async () => {
    while (root.firstChild) root.firstChild.remove();
    root.append(login);
    throw Object.assign(new Error("login required"), { status: 401 });
  } });
  assert.equal(root.firstChild, login);
  assert.equal(location.search, "?state=keep-me");
});

test("Alexa startup offers retry for network failures without changing OAuth parameters", async t => {
  const root = dom(t);
  let reloaded = false;
  globalThis.location = { search: "?state=keep-me", reload() { reloaded = true; } };
  await startAlexaLink({ reconnect: async () => { throw new Error("offline"); } });
  assert.ok(walk(root).some(node => node.textContent === "offline"));
  assert.equal(walk(root).some(node => node.textContent === "Alexaと連携する"), false);
  walk(root).find(node => node.tag === "button").listeners.click();
  assert.equal(reloaded, true);
  assert.equal(location.search, "?state=keep-me");
});

test("revocation failure leaves a usable retry button", async t => {
  dom(t);
  t.mock.method(globalThis, "fetch", async (_path, options) => options.method === "DELETE"
    ? new Response(JSON.stringify({ error: "try again" }), { status: 503 })
    : new Response(JSON.stringify({ enabled: true, connected: true })));
  const node = createAlexaConnection();
  await new Promise(resolve => setImmediate(resolve));
  const button = walk(node).find(node => node.tag === "button");
  const event = { currentTarget: button };
  const pending = button.listeners.click(event);
  event.currentTarget = null; // Native events clear currentTarget after dispatch.
  await pending;
  assert.equal(button.disabled, false);
  assert.ok(walk(node).some(node => node.textContent === "try again"));
});

test("the Japanese interaction model exposes all three supported actions", () => {
  const model = JSON.parse(readFileSync(new URL("../integrations/alexa/ja-JP.json", import.meta.url)));
  const intents = model.interactionModel.languageModel.intents;
  for (const name of ["ThrowKakeraIntent", "ReadRepliesIntent", "ReadSchedulesIntent", "AMAZON.StopIntent", "AMAZON.CancelIntent"]) assert.ok(intents.some(intent => intent.name === name));
  assert.equal(intents.find(intent => intent.name === "ThrowKakeraIntent").slots[0].type, "AMAZON.SearchQuery");
});

test("all English locales ship compatible voice actions and free-form note slots", () => {
  for (const locale of ["en-US", "en-GB", "en-CA", "en-AU", "en-IN"]) {
    const { interactionModel: { languageModel: model } } = JSON.parse(readFileSync(new URL(`../integrations/alexa/${locale}.json`, import.meta.url)));
    assert.equal(model.invocationName, "kakeratta notes");
    for (const name of ["ThrowKakeraIntent", "ReadRepliesIntent", "ReadSchedulesIntent"]) {
      const intent = model.intents.find(intent => intent.name === name);
      assert.ok(intent.samples.length > 0, `${locale}: ${name}`);
    }
    const post = model.intents.find(intent => intent.name === "ThrowKakeraIntent");
    assert.deepEqual(post.slots, [{ name: "text", type: "AMAZON.SearchQuery" }]);
    assert.ok(post.samples.includes("take a note {text}"));
  }
});

test("English account linking localizes consent and server errors", async t => {
  const root = dom(t);
  setLanguage("en");
  globalThis.location = { search: "?state=keep-me" };
  t.mock.method(globalThis, "fetch", async () => new Response(JSON.stringify({ error: "Alexaアプリから連携を開始してください" }), { status: 400 }));
  showAlexaLink();
  const button = walk(root).find(node => node.tag === "button");
  assert.equal(button.textContent, "Link with Alexa");
  assert.ok(walk(root).some(node => node.textContent.includes("Other people using this Alexa")));
  await button.listeners.click();
  assert.ok(walk(root).some(node => node.textContent === "Please start account linking from the Alexa app."));
  assert.equal(location.search, "?state=keep-me");
});

test("review sign-in preserves OAuth state and waits for a server session", async t => {
  const { reviewLoginURL, showReviewLogin } = await import("../frontend/src/review-login.js");
  const root = dom(t);
  const query = "?client_id=client&redirect_uri=https%3A%2F%2Fexample.com%2Fcallback&state=a%26b&response_type=code&scope=alexa&lang=en";
  const review = new URL(reviewLoginURL("/alexa", query), "https://kakeratta.net");
  assert.equal(review.searchParams.get("review"), "1");
  const back = new URL(reviewLoginURL(review.pathname, review.search, false), review);
  for (const [key, value] of new URLSearchParams(query)) assert.equal(back.searchParams.get(key), value);
  assert.equal(back.searchParams.has("review"), false);
  let finishSession, completed = false, calls = 0;
  const session = new Promise(resolve => { finishSession = resolve; });
  showReviewLogin({
    signIn: async (email, password) => { calls++; assert.equal(email, "review@example.com"); assert.equal(password, "test-password"); return { user: "reviewer" }; },
    establishSession: async user => { assert.equal(user, "reviewer"); await session; },
    onSuccess: () => { completed = true; }, back: back.pathname + back.search,
  });
  const nodes = walk(root);
  nodes.find(n => n.type === "email").value = "review@example.com";
  nodes.find(n => n.type === "password").value = "test-password";
  const form = nodes.find(n => n.tag === "form");
  const pending = form.listeners.submit({ preventDefault() {} });
  await form.listeners.submit({ preventDefault() {} });
  assert.equal(calls, 1);
  assert.equal(completed, false);
  finishSession(); await pending;
  assert.equal(completed, true);
});

test("review login failure keeps a retryable form without exposing credentials", async t => {
  const { showReviewLogin } = await import("../frontend/src/review-login.js");
  const root = dom(t);
  showReviewLogin({ signIn: async () => { throw Error("secret credential"); }, establishSession() {}, onSuccess() { assert.fail(); }, back: "/alexa" });
  const nodes = walk(root);
  nodes.find(n => n.type === "email").value = "review@example.com";
  nodes.find(n => n.type === "password").value = "password";
  await nodes.find(n => n.tag === "form").listeners.submit({ preventDefault() {} });
  assert.equal(nodes.find(n => n.tag === "button").disabled, false);
  assert.match(nodes.find(n => n.role === "status").textContent, /Unable to sign in/);
  assert.equal(nodes.some(n => n.textContent.includes("secret credential")), false);
});

test("all Alexa locales include manual text collection and confirmation commands", () => {
  for (const locale of ["ja-JP", "en-US", "en-GB", "en-CA", "en-AU", "en-IN"]) {
    const { interactionModel: model } = JSON.parse(readFileSync(new URL(`../integrations/alexa/${locale}.json`, import.meta.url), "utf8"));
    for (const name of ["AMAZON.YesIntent", "AMAZON.NoIntent", "ReviseKakeraIntent"]) {
      assert.ok(model.languageModel.intents.some(intent => intent.name === name), `${locale}: ${name}`);
    }
    assert.equal(model.dialog.delegationStrategy, "SKILL_RESPONSE");
    for (const name of ["ThrowKakeraIntent", "ReplyKakeraIntent"]) {
      const intent = model.languageModel.intents.find(intent => intent.name === name);
      assert.ok(intent.samples.some(sample => !sample.includes("{text}")), `${locale}: bare command`);
      const slot = model.dialog.intents.find(intent => intent.name === name).slots.find(slot => slot.name === "text");
      assert.ok(model.prompts.some(prompt => prompt.id === slot.prompts.elicitation));
    }
  }
});
