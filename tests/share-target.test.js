import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

for (const outcome of ["ok", "rejected", "offline"]) {
  test(`share target redirects after ${outcome}`, async () => {
    const handlers = {};
    const context = {
      self: { addEventListener: (name, handler) => { handlers[name] = handler; } },
      URL, Response,
      fetch: async () => {
        if (outcome === "offline") throw new Error("offline");
        return new Response("", { status: outcome === "ok" ? 200 : 401 });
      },
    };
    vm.runInNewContext(readFileSync(new URL("../frontend/sw.js", import.meta.url), "utf8"), context);
    let result;
    handlers.fetch({
      request: { method: "POST", url: "https://kakeratta.example/api/capture", formData: async () => new FormData() },
      respondWith: (promise) => { result = promise; },
    });
    const response = await result;
    assert.equal(response.status, 303);
    assert.equal(response.headers.get("location"), `https://kakeratta.example/?shared=${outcome === "ok" ? 1 : 0}`);
  });
}
