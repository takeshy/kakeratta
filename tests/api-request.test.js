import test from "node:test";
import assert from "node:assert/strict";
import { createAPIRequest } from "../frontend/src/api-request.js";
import { api } from "../frontend/src/api.js";

test("raw persona installation preserves the definition and uses the shared timeout", async (t) => {
  const definition = "---\nname: sample\n---\nInstructions";
  t.mock.method(globalThis, "fetch", async (path, options) => {
    assert.equal(path, "/api/personas/install");
    assert.equal(options.method, "POST");
    assert.equal(options.body, definition);
    assert.equal(options.headers["Content-Type"], undefined);
    assert.ok(options.signal instanceof AbortSignal);
    return Response.json({ id: "sample", active: true });
  });
  assert.deepEqual(await api.installPersona(definition), { id: "sample", active: true });
});

test("JSON and raw requests verify the account and send the workspace header", async () => {
  for (const raw of [false, true]) {
    let verified = false;
    const call = createAPIRequest({
      identity: () => ({ key: "alice" }),
      verify: async () => { verified = true; },
      fetch: async (_path, options) => {
        assert.equal(verified, true);
        assert.equal(options.headers["X-Kakeratta-Workspace"], "alice");
        assert.equal(options.body, raw ? "definition" : '{"text":"hello"}');
        return Response.json({ ok: true });
      },
    });
    assert.deepEqual(await call("POST", "/api/example", raw ? "definition" : { text: "hello" }, { raw }), { ok: true });
  }
});

test("account changes during verification prevent sending the request", async () => {
  let key = "alice";
  const call = createAPIRequest({
    identity: () => ({ key }),
    verify: async () => { key = "bob"; },
    fetch: async () => { assert.fail("must not send to another account"); },
  });
  await assert.rejects(call("POST", "/api/personas/install", "definition", { raw: true }), /Account changed/);
});

test("account changes while a response is pending prevent returning its data", async () => {
  let key = "alice";
  const call = createAPIRequest({
    identity: () => key ? { key } : null,
    verify: async () => {},
    fetch: async () => { key = null; return Response.json({ private: true }); },
  });
  await assert.rejects(call("GET", "/api/settings"), /Account changed/);
});

test("API errors retain their status; empty successful responses return null", async () => {
  const call = createAPIRequest({
    identity: () => null,
    verify: async () => { assert.fail("no account to verify"); },
    fetch: async (path) => path === "/empty" ? new Response(null, { status: 204 }) : Response.json({ error: "invalid definition" }, { status: 400 }),
  });
  await assert.rejects(call("POST", "/error"), { message: "invalid definition", status: 400 });
  assert.equal(await call("DELETE", "/empty"), null);
});
