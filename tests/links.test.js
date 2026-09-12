import test from "node:test";
import assert from "node:assert/strict";
import { safeLink } from "../frontend/src/dom.js";

test("post and source links only allow HTTP navigation", () => {
  for (const value of ["javascript:alert(1)", "java\nscript:alert(1)", "data:text/html,<script>alert(1)</script>"]) {
    assert.equal(safeLink(value), null);
  }
  for (const value of ["https://example.com/a", "http://example.com/", "/api/blob/capture.png"]) {
    assert.equal(safeLink(value), value);
  }
});
