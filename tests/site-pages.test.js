import test from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { setLanguage } from "../frontend/src/i18n.js";

const frontend = new URL("../frontend/", import.meta.url).pathname;
const read = (path) => readFileSync(join(frontend, path), "utf8");

// A capture's CSS size: the PNG is taken at 2x, so half its pixel size.
function captureSize(source) {
  const png = readFileSync(join(frontend, "..", "docs", "captures", source));
  return { width: png.readUInt32BE(16) / 2, height: png.readUInt32BE(20) / 2 };
}

// The landing page leads to the manual and the app; the manual leads back to
// both. App links explicitly request sign-in to avoid the guest landing-page
// redirect. Both pages carry both languages.
test("the landing page and the manual point at each other and at the app", () => {
  const lp = read("lp/index.html");
  const manual = read("manual/index.html");
  assert.match(lp, /href="\/manual\/"/);
  assert.match(lp, /href="\/\?login=1"/);
  assert.match(manual, /href="\/lp\/"/);
  assert.match(manual, /href="\/\?login=1"/);
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
  events = {};
  addEventListener(name, callback) { this.events[name] = callback; }
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
  const requested = [];
  let finishRequest;
  const [header] = screenNodes({ compose: new Stub(), room, feed: new Stub(), onOpen: () => {}, onLogout: () => {},
    onCheckIn: period => { requested.push(period); return new Promise(resolve => { finishRequest = resolve; }); },
  });
  const daily = header.find(node => node.className === "check-in-action" && node.find(child => child.textContent === "Run daily check-in now"));
  const monthly = header.find(node => node.className === "check-in-action" && node.find(child => child.textContent === "Run monthly check-in"));
  assert.ok(daily);
  assert.ok(monthly);
  assert.ok(header.find(node => node.tag === "button" && node.textContent === "Sign out"));
  for (const [button, period] of [[daily, "daily"], [monthly, "monthly"]]) {
    const pending = button.events.click();
    assert.equal(button.disabled, true, "pending request disables its button");
    assert.equal(requested.at(-1), period);
    finishRequest();
    await pending;
    assert.equal(button.disabled, false);
  }
  const ownStyles = header.find(node => node.tag === "style");
  assert.ok(ownStyles.textContent.includes(".nav-more-items { position: absolute;"), "popup layout ships with the component");
  const help = header.find((node) => node.className.includes("help"));
  assert.ok(help, "the header has a help link");
  assert.equal(help.tag, "a");
  assert.equal(help.attributes.href, "/manual/?lang=en");
  assert.equal(help.attributes.target, "_blank");
  assert.equal(help.attributes.rel, "noopener");
  assert.equal(help.textContent, "User guide");
  assert.ok(header.find(node => node.className === "nav-more"), "desktop offers the judging menu");
  assert.ok(header.find(node => node.className === "nav-secondary").find(node => node === help));
  assert.ok(header.find(node => node.attributes["aria-label"] === "Schedules"));

  const primary = node => node.className === "chip ghost nav nav-search";
  const desktopSearch = header.find(primary);
  assert.deepEqual(desktopSearch.children.map(node => node.className), ["nav-icon", "nav-label"]);
  const previousWidth = globalThis.innerWidth;
  t.after(() => {
    if (previousWidth === undefined) delete globalThis.innerWidth;
    else globalThis.innerWidth = previousWidth;
  });
  globalThis.innerWidth = 240;
  const [mobile] = screenNodes({ compose: new Stub(), room, feed: new Stub() });
  const mobileSearch = mobile.find(primary);
  assert.equal(mobileSearch.children.length, 1, "phones show the icon alone");
  assert.equal(mobileSearch.children[0].className, "nav-icon");
  assert.match(mobile.className, /compact narrow/);
  assert.equal(mobile.find(node => node.className === "chip ghost nav nav-settings"), null);
  assert.equal(mobile.find(node => node.className === "chip ghost nav nav-personas"), null);
  const mobileMore = mobile.find(node => node.className === "nav-more");
  const toggle = mobileMore.find(node => node.className.includes("more-toggle"));
  assert.equal(toggle.tag, "button");
  assert.equal(toggle.attributes["aria-expanded"], "false");
  assert.equal(toggle.attributes["aria-label"], "Judging menu");
  assert.equal((toggle.innerHTML.match(/<circle/g) || []).length, 3);
  assert.ok(mobileMore.find(node => node.className === "nav-more-settings"));
  assert.ok(mobileMore.find(node => node.className === "nav-more-personas"));
  assert.equal(mobileSearch.attributes["aria-label"], "Search");

  setLanguage("ja");
  assert.equal(manualLink(), "/manual/?lang=ja");
});

// Retaking a capture changes its height. An <img> still sized for the old one
// shows the new picture stretched, which is how a manual image went out
// squashed after its capture was retaken.
test("every screenshot is sized to the capture it shows", () => {
  for (const path of ["lp/index.html", "manual/index.html"]) {
    for (const [tag, source] of read(path).matchAll(/<img [^>]*src="\/captures\/([^"]+)"[^>]*>/g)) {
      const { width, height } = captureSize(source);
      assert.match(tag, new RegExp(`width="${width}"`), `${path} → ${source} width`);
      assert.match(tag, new RegExp(`height="${height}"`), `${path} → ${source} height`);
    }
  }
});

test("every internal anchor and toolLink target exists in the manual in both languages", async (t) => {
  globalThis.Node = Stub;
  globalThis.document = {
    createElement: (tag) => Object.assign(new Stub(), { tag }),
    createTextNode: (text) => Object.assign(new Stub(), { textContent: text }),
  };
  t.after(() => { delete globalThis.Node; delete globalThis.document; setLanguage("ja"); });
  const { toolLink } = await import("../frontend/src/persona-display.js");

  const manual = read("manual/index.html");
  const ids = new Set([...manual.matchAll(/\bid="([^"]+)"/g)].map((m) => m[1]));
  const jaIds = [...ids].filter((id) => id.startsWith("ja-")).map((id) => id.slice(3)).sort();
  const enIds = [...ids].filter((id) => id.startsWith("en-")).map((id) => id.slice(3)).sort();
  assert.deepEqual(jaIds, enIds, "Japanese and English sections carry matching anchor IDs");

  assert.doesNotMatch(manual, /href="\/manual\/#/, "same-document links inside the manual use #... directly");
  for (const [, target] of manual.matchAll(/href="#([^"]+)"/g)) {
    assert.ok(ids.has(target), `manual/index.html href="#${target}" resolves to an id`);
  }

  for (const lang of ["ja", "en"]) {
    setLanguage(lang);
    for (const tool of ["web_search", "place_search", "timeline_search", "notes", "records", "schedule", "weather_forecast", "unknown_tool"]) {
      const href = toolLink(tool).attributes.href;
      const hash = href.split("#")[1];
      assert.ok(ids.has(hash), `toolLink(${tool}) in ${lang} → #${hash} exists in manual/index.html`);
    }
  }
});

