import test from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { setLanguage } from "../frontend/src/i18n.js";

const frontend = new URL("../frontend/", import.meta.url).pathname;
const read = (path) => readFileSync(join(frontend, path), "utf8");

// The landing page leads to the manual and the app; the manual leads back to
// both. Both pages carry both languages.
test("the landing page and the manual point at each other and at the app", () => {
  const lp = read("lp/index.html");
  const manual = read("manual/index.html");
  assert.match(lp, /href="\/manual\/"/);
  assert.match(lp, /href="\/"/);
  assert.match(manual, /href="\/lp\/"/);
  assert.match(manual, /href="\/"/);
  for (const page of [lp, manual]) {
    assert.match(page, /class="ja"/);
    assert.match(page, /class="en"/);
    assert.match(page, /data-set-lang="ja"/);
    assert.match(page, /data-set-lang="en"/);
    assert.match(page, /\/site\/site\.css/);
    assert.match(page, /\/site\/lang\.js/);
  }
});

// The screenshots are the README's, copied at build time. A renamed capture
// would otherwise only be noticed as a broken image on the deployed page.
test("every screenshot the pages use exists in docs/captures", () => {
  for (const path of ["lp/index.html", "manual/index.html"]) {
    const sources = [...read(path).matchAll(/src="\/captures\/([^"]+)"/g)].map((m) => m[1]);
    assert.ok(sources.length > 0, `${path} shows screenshots`);
    for (const source of sources) {
      assert.ok(existsSync(join(frontend, "..", "docs", "captures", source)), `${path} → docs/captures/${source}`);
    }
  }
});

// A stub just deep enough for the header to be built: the real DOM is not
// available here, and what matters is which attributes the link carries.
class Stub {
  children = [];
  attributes = {};
  className = "";
  textContent = "";
  innerHTML = "";
  setAttribute(name, value) { this.attributes[name] = value; }
  addEventListener() {}
  append(...nodes) { this.children.push(...nodes); }
  find(predicate) {
    for (const child of this.children) {
      if (!(child instanceof Stub)) continue;
      if (predicate(child)) return child;
      const inner = child.find(predicate);
      if (inner) return inner;
    }
    return null;
  }
}

test("the header links to the manual in a new tab, in the reader's language", async (t) => {
  globalThis.Node = Stub;
  globalThis.document = {
    createElement: (tag) => Object.assign(new Stub(), { tag }),
    createTextNode: (text) => Object.assign(new Stub(), { textContent: text }),
  };
  t.after(() => { delete globalThis.Node; delete globalThis.document; setLanguage("ja"); });
  const { screenNodes, manualLink } = await import("../frontend/src/screen.js");

  setLanguage("en");
  assert.equal(manualLink(), "/manual/?lang=en");
  const room = { banner: new Stub(), bar: new Stub() };
  const [header] = screenNodes({ compose: new Stub(), room, feed: new Stub(), onOpen: () => {}, onLogout: null });
  const help = header.find((node) => node.className.includes("help"));
  assert.ok(help, "the header has a help link");
  assert.equal(help.tag, "a");
  assert.equal(help.attributes.href, "/manual/?lang=en");
  assert.equal(help.attributes.target, "_blank");
  assert.equal(help.attributes.rel, "noopener");
  assert.equal(help.attributes["aria-label"], "Help");
  assert.equal(help.attributes.title, "Manual (opens in a new tab)");
  assert.match(help.innerHTML, /<svg/);

  setLanguage("ja");
  assert.equal(manualLink(), "/manual/?lang=ja");
});
