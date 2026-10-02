import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { enablePush, disablePush, revokeDevicePush } from "../frontend/src/push-client.js";

function browser(t, { saved = null, permission = "granted" } = {}) {
  let unsubscribed = 0;
  let created = 0;
  const subscription = { endpoint: "https://fcm.googleapis.com/push/test", toJSON: () => ({ endpoint: "https://fcm.googleapis.com/push/test", keys: {} }), unsubscribe: async () => { unsubscribed++; return true; } };
  const registration = { pushManager: { getSubscription: async () => saved, subscribe: async options => { assert.equal(options.userVisibleOnly, true); created++; return subscription; } }, getNotifications: async () => [] };
  for (const [key, value] of Object.entries({ navigator: { serviceWorker: { register: async () => registration, ready: Promise.resolve(registration), getRegistration: async () => registration } }, Notification: { requestPermission: async () => permission } })) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, { configurable: true, value });
    t.after(() => descriptor ? Object.defineProperty(globalThis,key,descriptor) : delete globalThis[key]);
  }
  return { subscription, stats: () => ({ unsubscribed, created }) };
}

test("enabling saves a browser subscription and rolls back an unsuccessful registration", async t => {
  const mock = browser(t);
  let saved;
  await enablePush({ savePush: async body => { saved = body; } }, "AQID");
  assert.equal(saved.endpoint, mock.subscription.endpoint);
  await assert.rejects(enablePush({ savePush: async () => { throw Error("offline"); } }, "AQID"), /offline/);
  assert.deepEqual(mock.stats(), { created: 2, unsubscribed: 1 });
});

test("denied permission never subscribes or saves", async t => {
  const mock = browser(t, { permission: "denied" });
  await assert.rejects(enablePush({ savePush: () => assert.fail("saved") }, "AQID"));
  assert.deepEqual(mock.stats(), { created: 0, unsubscribed: 0 });
});

test("disable removes the server subscription before revoking the browser", async t => {
  const calls = [];
  browser(t, { saved: { endpoint: "https://fcm.googleapis.com/x", unsubscribe: async () => { calls.push("browser"); return true; } } });
  await disablePush({ deletePush: async body => { assert.equal(body.endpoint,"https://fcm.googleapis.com/x"); calls.push("server"); } });
  assert.deepEqual(calls, ["server", "browser"]);
});

test("offline logout still revokes the browser subscription", async t => {
  let revoked = false;
  browser(t, { saved: { endpoint: "https://fcm.googleapis.com/x", unsubscribe: async () => { revoked = true; return true; } } });
  t.mock.method(globalThis, "fetch", async () => { throw Error("offline"); });
  await revokeDevicePush("alice");
  assert.equal(revoked, true);
});

function worker(account = "alice") {
  const listeners = {};
  const notices = [];
  const opened = [];
  const self = { location: { origin: "https://app.example" }, addEventListener: (name, callback) => { listeners[name] = callback; }, registration: { showNotification: async (title, options) => { notices.push({ title, ...options }); } }, clients: { matchAll: async () => [], openWindow: async url => { opened.push(url); } } };
  vm.runInNewContext(readFileSync(new URL("../frontend/public/sw.js", import.meta.url),"utf8"), { self, URL, AbortSignal, fetch: async () => ({ ok: true, json: async () => ({ key: account }) }) });
  const dispatch = async (name, props) => { let work; listeners[name]({ ...props, waitUntil: promise => { work = promise; } }); await work; };
  return { notices, opened, dispatch };
}

test("service worker displays only the active account's reminders and opens the post", async () => {
  const w = worker();
  await w.dispatch("push", { data: { json: () => ({ entry: "e_private", workspace: "bob" }) } });
  assert.equal(w.notices.length, 0);
  await w.dispatch("push", { data: { json: () => ({ entry: "e_1", workspace: "alice", title: "餌やりの時間です。金魚に餌をあげてください。", body: "kakeratta" }) } });
  assert.equal(w.notices.length, 1);
  assert.equal(w.notices[0].title, "餌やりの時間です。金魚に餌をあげてください。");
  assert.equal(w.notices[0].body, "kakeratta");
  assert.equal(w.notices[0].tag,"reminder:e_1");
  await w.dispatch("notificationclick", { notification: { data: w.notices[0].data, close() {} } });
  assert.deepEqual(w.opened, ["https://app.example/?entry=e_1"]);
});

test("clicking an old account's notification does not open a post", async () => {
  const w = worker("bob");
  await w.dispatch("notificationclick", { notification: { data: { entry: "e_old", workspace: "alice" }, close() {} } });
  assert.equal(w.opened.length,0);
});
