import test from "node:test";
import assert from "node:assert/strict";
import { preserveScroll } from "../frontend/src/dom.js";

test("scroll preservation restores nested containers and document after a rebuild clamps them", t => {
  const root = { scrollTop: 700, scrollLeft: 0 };
  const sheet = { scrollTop: 450, scrollLeft: 5, parentElement: root };
  const view = { scrollTop: 0, scrollLeft: 0, parentElement: sheet };
  const previous = globalThis.document;
  globalThis.document = { scrollingElement: root };
  t.after(() => { if (previous) globalThis.document = previous; else delete globalThis.document; });
  preserveScroll(view, () => { sheet.scrollTop = 0; sheet.scrollLeft = 0; root.scrollTop = 0; });
  assert.equal(sheet.scrollTop, 450);
  assert.equal(sheet.scrollLeft, 5);
  assert.equal(root.scrollTop, 700);
});
