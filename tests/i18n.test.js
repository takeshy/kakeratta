import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";

import { getLanguage, resolveLanguage, setLanguage, t, translateError } from "../frontend/src/i18n.js";
import { createBrowserRecognizer } from "../frontend/src/browser_speech.js";

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

test("static UI translation calls and labels have English translations", () => {
  setLanguage("en");
  const root = new URL("../frontend/src/", import.meta.url);
  const missing = [];
  for (const file of readdirSync(root).filter(file => file.endsWith(".js") && file !== "i18n.js")) {
    const source = readFileSync(new URL(file, root), "utf8");
    // Include concatenated literals, such as the persona panel introduction.
    const calls = /\b(?:t\(\s*|(?:label|i18nTitle|i18nPlaceholder)\s*:\s*)((?:"(?:\\.|[^"\\])*"\s*\+\s*)*"(?:\\.|[^"\\])*")/g;
    for (const match of source.matchAll(calls)) {
      const text = [...match[1].matchAll(/"(?:\\.|[^"\\])*"/g)].map(part => JSON.parse(part[0])).join("");
      if (/[ぁ-んァ-ヶ一-龯]/u.test(text) && t(text) === text) missing.push(`${file}: ${text}`);
    }
    for (const match of source.matchAll(/\bError\(\s*("(?:\\.|[^"\\])*")/g)) {
      const text = JSON.parse(match[1]);
      if (/[ぁ-んァ-ヶ一-龯]/u.test(text)) missing.push(`${file}: untranslated error: ${text}`);
    }
  }
  assert.deepEqual(missing, []);
});

test("browser API handlers' literal Japanese errors have English translations", () => {
  setLanguage("en");
  const root = new URL("../internal/server/", import.meta.url);
  const missing = [];
  for (const file of readdirSync(root).filter(file => file.endsWith(".go") && !file.endsWith("_test.go"))) {
    const source = readFileSync(new URL(file, root), "utf8");
    for (const match of source.matchAll(/\bfail\(w,\s*[^,\n]+,\s*("(?:\\.|[^"\\])*")\s*\)/g)) {
      const text = JSON.parse(match[1]);
      if (/[ぁ-んァ-ヶ一-龯]/u.test(text) && translateError(text) === text) missing.push(`${file}: ${text}`);
    }
  }
  assert.deepEqual(missing, []);
});

test("application errors translate dynamic values while preserving unknown details", () => {
  setLanguage("en");
  assert.equal(translateError("取得できませんでした (403)"), "Could not fetch the URL (403).");
  assert.equal(translateError("いまの事実が合わせて9000字になります（8000字まで）"), "The facts total 9000 characters (maximum 8000).");
  assert.equal(translateError("メモの名前が不正です: 日本旅行.md"), "The note name is invalid: 日本旅行.md");
  assert.equal(translateError("担当は保存しましたが、通知予定を登録できませんでした：予定が見つかりません"), "The persona was saved, but its notification schedule could not be registered: Schedule not found.");
  assert.equal(translateError("Unknown provider detail: 日本旅行"), "Unknown provider detail: 日本旅行");
  setLanguage("ja");
  assert.equal(translateError("取得できませんでした (403)"), "取得できませんでした (403)");
});

test("speech recognition errors are English even with Japanese speech selected", context => {
  setLanguage("en");
  let recognition;
  const previous = Object.getOwnPropertyDescriptor(globalThis, "SpeechRecognition");
  context.after(() => previous ? Object.defineProperty(globalThis, "SpeechRecognition", previous) : delete globalThis.SpeechRecognition);
  Object.defineProperty(globalThis, "SpeechRecognition", { configurable: true, value: class {
    constructor() { recognition = this; }
    start() {}
    stop() {}
    abort() {}
  } });
  const recognizer = createBrowserRecognizer({ getSettings: () => ({ language: "ja" }),
    getBase: () => "", onInput() {}, onSend() {}, onState() {} });
  recognizer.toggle();
  recognition.onerror({ error: "service-not-allowed" });
  assert.equal(recognizer.state().error, "Speech recognition failed: The speech recognition service is unavailable. (service-not-allowed)");
});
