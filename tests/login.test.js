import test from "node:test";
import assert from "node:assert/strict";
import { createGoogleLogin, guestLandingPage, loginView } from "../frontend/src/login.js";
import { setLanguage } from "../frontend/src/i18n.js";
import { readFileSync } from "node:fs";

test("guest top-page visits go to the localized landing page", () => {
  assert.equal(guestLandingPage("/"), "/lp/");
  assert.equal(guestLandingPage("/index.html", "?lang=en"), "/lp/?lang=en");
  assert.equal(guestLandingPage("/", "?lang=ja&unrelated=value"), "/lp/?lang=ja");
  assert.equal(guestLandingPage("/", "?lang=invalid"), "/lp/");
  assert.equal(guestLandingPage("/judge/invitation"), "");
});

test("every LP and manual app link reaches sign-in without a redirect loop", () => {
  for (const page of ["lp", "manual"]) {
    const html = readFileSync(new URL(`../frontend/${page}/index.html`, import.meta.url), "utf8");
    const links = [...html.matchAll(/<a\b[^>]*href="([^"]+)"[^>]*>/g)]
      .map(match => new URL(match[1], `https://kakeratta.net/${page}/`))
      .filter(url => url.origin === "https://kakeratta.net" && url.pathname === "/");
    assert.ok(links.length, `${page} needs an app link`);
    for (const url of links) {
      assert.equal(guestLandingPage(url.pathname, url.search), "");
      url.searchParams.set("lang", "en");
      assert.equal(guestLandingPage(url.pathname, url.search), "");
    }
  }
});

const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; };

test("login remains in progress after Google approval until the server session finishes", async () => {
  const google = deferred(), session = deferred();
  const shown = [];
  let calls = 0, completed = false, sessionUser;
  const login = createGoogleLogin({
    signIn: () => { calls++; return google.promise; },
    establishSession: user => { sessionUser = user; return session.promise; },
    onSuccess: () => { completed = true; },
    show: state => shown.push(state),
  });
  const pending = login();
  assert.equal(shown.at(-1).state, "authorizing");
  await login();
  assert.equal(calls, 1, "another login cannot start while the first is pending");
  const user = { uid: "reader" };
  google.resolve({ user });
  await Promise.resolve();
  assert.equal(shown.at(-1).state, "completing");
  assert.equal(sessionUser, user);
  assert.equal(completed, false);
  assert.equal(shown.some(view => !view.state), false, "the login screen must not reappear after approval");
  session.resolve(); await pending;
  assert.equal(completed, true);
  assert.deepEqual(shown.map(view => view.state), ["authorizing", "completing"]);
});

test("a failed server session restores a working login action with an error", async () => {
  const shown = [];
  let calls = 0, fail = true, completed = false;
  const login = createGoogleLogin({
    signIn: async () => { calls++; return { user: {} }; },
    establishSession: async () => { if (fail) throw Error("session failed"); },
    onSuccess: () => { completed = true; },
    show: state => shown.push(state),
  });
  await login();
  assert.equal(shown.at(-1).error, "session failed");
  assert.equal(shown.at(-1).onLogin, login);
  assert.equal(completed, false);
  fail = false;
  await shown.at(-1).onLogin();
  assert.equal(calls, 2);
  assert.equal(completed, true);
});

test("cancelling Google approval restores login without creating a session", async () => {
  let view, session = false;
  await createGoogleLogin({
    signIn: async () => { throw Error("popup closed"); },
    establishSession: async () => { session = true; },
    onSuccess: () => assert.fail("cancelled login succeeded"),
    show: state => { view = state; },
  })();
  assert.equal(view.error, "popup closed");
  assert.equal(typeof view.onLogin, "function");
  assert.equal(session, false);
});

class Element {
  children = []; textContent = "";
  constructor(tag) { this.tag = tag; }
  setAttribute(name, value) { this[name] = value; }
  addEventListener() {}
  append(...nodes) { this.children.push(...nodes); }
}
const walk = node => [node, ...node.children.flatMap(walk)];
test("pending views announce progress and never show a login button in either language", t => {
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: text => Object.assign(new Element("text"), { textContent: text }) };
  t.after(() => { delete globalThis.Node; delete globalThis.document; });
  for (const language of ["ja", "en"]) {
    setLanguage(language);
    for (const state of ["checking", "authorizing", "completing", "loading"]) {
      const view = loginView({ state });
      assert.equal(view["aria-busy"], "true");
      assert.equal(walk(view).some(node => node.tag === "button"), false);
      assert.equal(walk(view).some(node => node.role === "status" && node["aria-live"] === "polite"), true);
    }
    assert.equal(walk(loginView()).some(node => node.tag === "button"), true);
  }
});
