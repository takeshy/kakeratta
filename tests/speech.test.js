import assert from "node:assert/strict";
import test from "node:test";
import { setLanguage } from "../frontend/src/i18n.js";
setLanguage("ja");
import { speechDraft } from "../frontend/src/speech.js";

test("speechDraft appends with a separating space", () => {
  assert.deepEqual(speechDraft("", "こんにちは", true, "over"), { text: "こんにちは", send: false });
  assert.deepEqual(speechDraft("hello", "world", true, "over"), { text: "hello world", send: false });
  assert.deepEqual(speechDraft("hello ", "world", true, "over"), { text: "hello world", send: false });
});

test("speechDraft strips a trailing send phrase only when final", () => {
  assert.deepEqual(speechDraft("", "送ります オーバー", true, "over, オーバー"), {
    text: "送ります",
    send: true
  });
  assert.deepEqual(speechDraft("", "send it over.", true, "over, オーバー"), {
    text: "send it",
    send: true
  });
  // Interim results must never trigger the send.
  assert.equal(speechDraft("", "send it over", false, "over").send, false);
  // A word merely containing the phrase does not send.
  assert.equal(speechDraft("", "the leftover", true, "over").send, false);
  // An empty phrase list disables the feature entirely.
  assert.equal(speechDraft("", "over", true, "").send, false);
});

test("question and Enter commands convert on final recognition, without needing silence", () => {
  for (const command of ["クエスチョン", "クエスチョンマーク", "question", "Question", "question mark"]) {
    for (const silence of [true, false]) {
      assert.equal(speechDraft("", `hello ${command}。`, true, "", silence).text, "hello?");
      assert.equal(speechDraft("", command, false, "", silence).text, command);
    }
  }
  assert.equal(speechDraft("", "いいですかクエスチョン。", true, "").text, "いいですか?");
  for (const word of ["まる", "丸", "句点", "くてん", "てん", "読点", "カンマ", "ピリオド",
    "はてな", "改行", "exclamation", "comma", "period", "new line", "disenter", "enters",
    "subquestion", "questions", "クエスチョンについて話す"]) {
    assert.equal(speechDraft("", word, true, "", true).text, word);
  }
  assert.deepEqual(speechDraft("", "hello question over", true, "over", true), { text: "hello question", send: true });
});

test("recognizer punctuation is preserved verbatim", () => {
  for (const text of ["きょうは。", "きょうは。晴れです。", "きょうは。、", "Hello.", "晴れです丸", "句点。"]) {
    assert.equal(speechDraft("", text, true, "").text, text);
  }
  assert.equal(speechDraft("前の入力。", "次の入力。", true, "").text, "前の入力。 次の入力。");
  assert.equal(speechDraft("きょうは。", "、", true, "").text, "きょうは。、");
});

test("spoken Enter inserts a line break and preserves the preceding punctuation", () => {
  for (const command of ["エンター", "enter", "Enter", "ENTER"]) {
    assert.deepEqual(speechDraft("前の文。", command + "。", true, ""), { text: "前の文。\n", send: false });
    assert.equal(speechDraft("", "前の文。 " + command + "。", true, "").text, "前の文。\n");
    assert.equal(speechDraft("", command, false, "").text, command);
  }
  assert.equal(speechDraft("", "エンターについて話す", true, "").text, "エンターについて話す");
  assert.equal(speechDraft("", "改行", true, "").text, "改行");
  assert.equal(speechDraft("", "new line", true, "").text, "new line");
});
