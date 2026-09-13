import assert from "node:assert/strict";
import test from "node:test";
import { chooseVoice } from "../frontend/src/speak.js";

const voices = [
  { name: "Korean", lang: "ko-KR", default: true },
  { name: "Japanese", lang: "ja-JP", default: false },
  { name: "English", lang: "en-US", default: false },
];

test("read aloud chooses a voice matching the interface language", () => {
  assert.equal(chooseVoice(voices, "", "ja"), voices[1]);
  assert.equal(chooseVoice(voices, "", "en"), voices[2]);
});

test("a saved voice from another language cannot override Japanese", () => {
  assert.equal(chooseVoice(voices, "Korean", "ja"), voices[1]);
});

test("underscore language tags used by some devices are accepted", () => {
  const japanese = { name: "System Japanese", lang: "ja_JP", default: false };
  assert.equal(chooseVoice([voices[0], japanese], "", "ja"), japanese);
});
