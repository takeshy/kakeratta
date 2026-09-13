import test from "node:test";
import assert from "node:assert/strict";

import { SILENCE_NOTE_UNTIL, showsSilenceNote } from "../frontend/src/feed.js";

// Silence is the design. A timeline with nothing in it yet cannot show that,
// so the note is said while the timeline is new and stops once it is not.
test("the silence note is for a new timeline only", () => {
  assert.equal(showsSilenceNote(0), false, "the empty state says it itself");
  assert.equal(showsSilenceNote(1), true);
  assert.equal(showsSilenceNote(SILENCE_NOTE_UNTIL - 1), true);
  assert.equal(showsSilenceNote(SILENCE_NOTE_UNTIL), false);
  assert.equal(showsSilenceNote(12), false);
});
