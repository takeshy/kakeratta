import test from "node:test";
import assert from "node:assert/strict";
import { createInstallPromptController } from "../frontend/src/install-prompt.js";

function setup({ ua = "Android Mobile", standalone = false, storage = new Map(), secure = true } = {}) {
  const browser = new EventTarget();
  const display = new EventTarget();
  display.matches = standalone;
  Object.assign(browser, {
    navigator: { userAgent: ua }, isSecureContext: secure,
    location: { href: "https://kakeratta.example/?room=1" },
    matchMedia: () => display,
    localStorage: { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value) },
  });
  let time = 1000, state;
  const controller = createInstallPromptController(browser, () => time);
  controller.subscribe(value => { state = value; });
  function offer(outcome = "accepted", prompt = async () => {}) {
    const event = new Event("beforeinstallprompt", { cancelable: true });
    Object.assign(event, { prompt, userChoice: Promise.resolve({ outcome }) });
    browser.dispatchEvent(event);
    return event;
  }
  return { browser, display, controller, offer, state: () => state, advance: () => { time += 8 * 86400000; } };
}

test("mobile captures an early install event and prompts once on user action", async () => {
  const app = setup();
  let calls = 0;
  assert.equal(app.state(), null);
  assert.equal(app.offer("accepted", async () => { calls++; }).defaultPrevented, true);
  app.controller.subscribe(value => assert.equal(value, calls ? null : "install"));
  assert.equal(calls, 0);
  await Promise.all([app.controller.install(), app.controller.install()]);
  assert.equal(calls, 1);
});

test("iPhone gets instructions, while desktop, tablets and standalone stay hidden", () => {
  assert.equal(setup({ ua: "iPhone" }).state(), "ios");
  for (const options of [{ ua: "Macintosh" }, { ua: "iPad" }, { ua: "Android" }, { standalone: true }, { secure: false }]) {
    const app = setup(options);
    assert.equal(app.offer().defaultPrevented, false);
    assert.equal(app.state(), null);
  }
  const app = setup({ ua: "iPhone" });
  app.browser.navigator.standalone = true;
  app.display.dispatchEvent(new Event("change"));
  assert.equal(app.state(), null);
});

test("dismissal persists for seven days and storage failure is harmless", () => {
  const storage = new Map();
  const app = setup({ ua: "iPhone", storage });
  app.controller.dismiss();
  assert.equal(app.state(), null);
  assert.equal(setup({ ua: "iPhone", storage }).state(), null);
  app.advance();
  app.display.dispatchEvent(new Event("change"));
  assert.equal(app.state(), "ios");
  app.browser.localStorage.setItem = () => { throw Error("blocked"); };
  app.controller.dismiss();
  assert.equal(app.state(), null);
});

test("cancelled or failed native prompts are consumed and snoozed", async () => {
  for (const fails of [false, true]) {
    const app = setup();
    let calls = 0;
    app.offer("dismissed", async () => { calls++; if (fails) throw Error("unavailable"); });
    await app.controller.install();
    await app.controller.install();
    assert.equal(calls, 1);
    assert.equal(app.state(), null);
    app.offer();
    assert.equal(app.state(), null);
  }
});

test("installation and standalone transitions hide the banner", () => {
  const app = setup();
  app.offer();
  app.display.matches = true;
  app.display.dispatchEvent(new Event("change"));
  assert.equal(app.state(), null);
  app.display.matches = false;
  app.browser.dispatchEvent(new Event("appinstalled"));
  assert.equal(app.state(), null);
  app.offer();
  assert.equal(app.state(), null);
});

test("in-app browsers ask to reopen, with a direct link from LINE", () => {
  const line = setup({ ua: "iPhone Safari Line/14.0.0" });
  assert.equal(line.state(), "in-app");
  assert.equal(line.controller.externalURL(), "https://kakeratta.example/?room=1&openExternalBrowser=1");
  const instagram = setup({ ua: "Android Mobile Instagram 300.0" });
  instagram.offer();
  assert.equal(instagram.state(), "in-app");
  assert.equal(instagram.controller.externalURL(), null);
  assert.equal(setup({ ua: "iPhone" }).controller.externalURL(), null);
});

test("Settings keeps the mode after Later, and listeners can leave", () => {
  const app = setup({ ua: "iPhone" });
  const seen = [];
  const leave = app.controller.subscribe(value => seen.push(value));
  app.controller.dismiss();
  assert.equal(app.state(), null);
  assert.equal(app.controller.mode(), "ios");
  leave();
  app.display.dispatchEvent(new Event("change"));
  assert.deepEqual(seen, ["ios", null]);
  app.browser.navigator.standalone = true;
  assert.equal(app.controller.mode(), null);
});

test("a Home Screen launch keeps later browser visits quiet", () => {
  const storage = new Map();
  const icon = setup({ ua: "Android Mobile", standalone: true, storage });
  assert.equal(icon.controller.standalone(), true);
  const tab = setup({ ua: "Android Mobile", storage });
  tab.offer();
  assert.equal(tab.state(), null);
  assert.equal(tab.controller.mode(), null);
});

test("the account's Home Screen launch hides the iPhone invitation", () => {
  const storage = new Map();
  const app = setup({ ua: "iPhone", storage });
  assert.equal(app.state(), "ios");
  assert.equal(app.controller.standalone(), false);
  app.controller.markInstalled();
  assert.equal(app.state(), null);
  assert.equal(app.controller.mode(), null);
  assert.equal(setup({ ua: "iPhone", storage }).state(), null);
});
