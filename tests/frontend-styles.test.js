import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const styles = readFileSync(new URL("../frontend/src/styles.css", import.meta.url), "utf8");

test("import review stays usable with a long title on a short viewport", () => {
  const closeRule = styles.match(/\.dialog-close\s*\{([^}]*)\}/)?.[1] || "";
  assert.match(closeRule, /align-self:\s*start/);

  const reviewRule = styles.match(/\.gmail-import-dialog\s*\{([^}]*)\}/)?.[1] || "";
  assert.match(reviewRule, /max-height:\s*calc\(100dvh\s*-\s*32px\)/);
  assert.match(reviewRule, /overflow-y:\s*auto/);
});
