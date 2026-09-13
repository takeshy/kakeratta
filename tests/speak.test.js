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

test("switching threads ignores delayed completion from the previous speech", async t => {
  const { createSpeaker } = await import("../frontend/src/speak.js");
  const utterances = [];
  globalThis.window = {
    SpeechSynthesisUtterance: class { constructor(text) { this.text = text; } },
    speechSynthesis: { getVoices: () => voices, cancel() {}, speak: utterance => utterances.push(utterance) },
  };
  globalThis.SpeechSynthesisUtterance = window.SpeechSynthesisUtterance;
  const speaker = createSpeaker();
  t.after(() => { speaker.stop(); delete globalThis.window; delete globalThis.SpeechSynthesisUtterance; });
  await speaker.start([{ id: "a", text: "old thread" }]);
  speaker.stop();
  await speaker.start([{ id: "b", text: "new post" }, { id: "c", text: "new reply" }]);
  utterances[0].onend();
  assert.equal(utterances.length, 2);
  assert.equal(speaker.speaking(), true);
  utterances[1].onend();
  assert.equal(utterances[2].text, "new reply");
  utterances[2].onend();
  assert.equal(speaker.speaking(), false);
});
