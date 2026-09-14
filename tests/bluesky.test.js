import test from "node:test";
import assert from "node:assert/strict";
import { api } from "../frontend/src/api.js";
import { finishBlueskyConnection } from "../frontend/src/bluesky.js";

test("OAuth callback sends the tab secret and immediately removes the code from the URL", async (t) => {
  const params = "state=state&iss=https%3A%2F%2Fauth.example.com&code=code";
  const location = { hash: `#bluesky-callback=${encodeURIComponent(params)}`, pathname: "/", search: "" };
  let cleaned = false, secretRemoved = false, payload;
  const previous = new Map(["location", "history", "sessionStorage", "document", "setTimeout"].map((key) => [key, globalThis[key]]));
  const node = () => ({ setAttribute() {}, append() {}, remove() {} });
  globalThis.location = location;
  globalThis.history = { replaceState(_a, _b, url) { assert.equal(url, "/"); cleaned = true; } };
  globalThis.sessionStorage = { getItem() { return "browser-secret"; }, removeItem() { secretRemoved = true; } };
  globalThis.document = { querySelector: node, createElement: node };
  globalThis.setTimeout = () => 0;
  t.after(() => { for (const [key, value] of previous) { if (value === undefined) delete globalThis[key]; else globalThis[key] = value; } });
  t.mock.method(api, "finishBluesky", async (body) => {
    assert.equal(cleaned, true);
    assert.equal(secretRemoved, true);
    payload = body;
    return { handle: "alice.bsky.social", returnRoot: "p" };
  });
  const result = await finishBlueskyConnection();
  assert.deepEqual(payload, { params, browserSecret: "browser-secret" });
  assert.equal(result.returnRoot, "p");
});
