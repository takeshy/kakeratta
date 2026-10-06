import test from "node:test";
import assert from "node:assert/strict";

test("story API fixtures preserve Storybook asset loading across repeated renders", async t => {
  const originalFetch = globalThis.fetch;
  const requests = [];
  globalThis.location = { origin: "http://127.0.0.1:6020" };
  globalThis.document = { querySelectorAll: () => [] };
  globalThis.EventSource = class { close() {} };
  globalThis.fetch = async input => { requests.push(input); return new Response("preview module"); };
  t.after(() => {
    globalThis.fetch = originalFetch;
    delete globalThis.location;
    delete globalThis.document;
    delete globalThis.EventSource;
  });
  const { serve } = await import("../frontend/stories/api.js");
  serve({ settings: { profile: { name: "sample" } } });
  assert.equal(await (await fetch("/vite-app.js")).text(), "preview module");
  assert.equal((await (await fetch("/api/settings")).json()).profile.name, "sample");
  serve({ settings: { profile: { name: "next" } } });
  assert.equal(await (await fetch("/@vite/client")).text(), "preview module");
  assert.equal((await (await fetch("/api/settings")).json()).profile.name, "next");
  assert.equal((await fetch("/api/unknown")).status, 404);
  assert.deepEqual(requests, ["/vite-app.js", "/@vite/client"]);
});
