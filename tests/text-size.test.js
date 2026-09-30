import test from "node:test";
import assert from "node:assert/strict";

const store = new Map();
globalThis.localStorage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) };
const root = { style: { values: {}, setProperty(k, v) { this.values[k] = v; } } };
globalThis.document = { documentElement: root };

const { applyTextSize, getTextSize, setTextSize } = await import("../frontend/src/text-size.js");

test("the text size is standard until one is chosen, and is kept", () => {
  assert.equal(getTextSize(), "standard");
  assert.equal(setTextSize("large"), "large");
  assert.equal(getTextSize(), "large");
  assert.equal(root.style.values["font-size"], "115%");
});

test("an unknown size falls back to standard", () => {
  store.set("kakeratta.textSize", "huge");
  assert.equal(getTextSize(), "standard");
  applyTextSize();
  assert.equal(root.style.values["font-size"], "100%");
});
