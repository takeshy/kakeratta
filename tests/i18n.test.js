import test from "node:test";
import assert from "node:assert/strict";

import { getLanguage, resolveLanguage, setLanguage, t } from "../frontend/src/i18n.js";

test("Japanese and English can be selected", () => {
  setLanguage("en");
  assert.equal(getLanguage(), "en");
  assert.equal(t("設定"), "Settings");
  assert.equal(t("起きた回数 {0}", 3), "Woken 3 times");
  assert.equal(t("起きた回数 1"), "Woken once");

  setLanguage("ja");
  assert.equal(getLanguage(), "ja");
  assert.equal(t("設定"), "設定");
});

test("an unsupported selection falls back to English", () => {
  setLanguage("fr");
  assert.equal(getLanguage(), "en");
});

test("language resolution uses Japanese only for a Japanese OS locale", () => {
  const previousNavigator = globalThis.navigator;
  const previousStorage = globalThis.localStorage;
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: () => null } });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { languages: ["ja-JP", "en-US"] } });
  assert.equal(resolveLanguage(), "ja");
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { languages: ["fr-FR"] } });
  assert.equal(resolveLanguage(), "en");
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { languages: ["en-US", "ja-JP"] } });
  assert.equal(resolveLanguage(), "en");
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: previousNavigator });
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: previousStorage });
});
