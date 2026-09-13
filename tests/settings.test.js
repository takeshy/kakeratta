import test from "node:test";
import assert from "node:assert/strict";
import { aiConfigured } from "../frontend/src/settings.js";

test("remote AI setup requires an endpoint, low model, and API key", () => {
  const ready = { provider: "nebius", baseUrl: "https://api.example/v1", lowModel: "small", apiKey: "********" };
  assert.equal(aiConfigured(ready), true);
  assert.equal(aiConfigured({ ...ready, apiKey: "" }), false);
  assert.equal(aiConfigured({ ...ready, lowModel: "" }), false);
  assert.equal(aiConfigured({ ...ready, baseUrl: "" }), false);
});
