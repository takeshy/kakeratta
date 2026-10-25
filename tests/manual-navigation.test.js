import test from "node:test";
import assert from "node:assert/strict";

test("manual language switches retain the section and other query parameters", async (t) => {
  const names = ["document", "window", "location", "history", "localStorage"];
  const previous = new Map(names.map(name => [name, Object.getOwnPropertyDescriptor(globalThis, name)]));
  t.after(() => {
    for (const name of names) {
      const descriptor = previous.get(name);
      if (descriptor) Object.defineProperty(globalThis, name, descriptor);
      else delete globalThis[name];
    }
  });

  const listeners = new Map();
  const addEventListener = (type, listener) => {
    if (!listeners.has(type)) listeners.set(type, []);
    listeners.get(type).push(listener);
  };
  const targets = new Map();
  for (const language of ["ja", "en"]) {
    targets.set(`${language}-google`, { id: `${language}-google`, scrolls: 0, scrollIntoView() { this.scrolls++; } });
  }
  const storage = new Map();
  globalThis.location = new URL("https://kakeratta.example/manual/?lang=en&keep=1#ja-google");
  globalThis.history = { replaceState(_state, _unused, url) { globalThis.location = new URL(url); } };
  globalThis.localStorage = { getItem: key => storage.get(key), setItem: (key, value) => storage.set(key, value) };
  globalThis.window = { addEventListener };
  globalThis.document = {
    documentElement: { lang: "ja", dataset: { titleJa: "kakeratta 使い方", titleEn: "kakeratta Manual" } },
    querySelectorAll: () => [],
    getElementById: id => targets.get(id),
    addEventListener,
  };

  await import("../frontend/site/manual.js");
  assert.equal(document.documentElement.lang, "en");
  assert.equal(document.title, "kakeratta Manual");
  assert.equal(location.hash, "#en-google");
  assert.equal(targets.get("en-google").scrolls, 1);

  const button = { dataset: { setLang: "ja" } };
  const click = { target: { closest: () => button } };
  for (const listener of listeners.get("click")) listener(click);
  assert.equal(document.documentElement.lang, "ja");
  assert.equal(document.title, "kakeratta 使い方");
  assert.equal(location.hash, "#ja-google");
  assert.equal(targets.get("ja-google").scrolls, 1);
  assert.equal(location.searchParams.get("keep"), "1");

  // A same-document link with the other language's anchor follows its
  // equivalent in the language the reader selected.
  location.hash = "#en-google";
  for (const listener of listeners.get("hashchange")) listener();
  assert.equal(location.hash, "#ja-google");
  assert.equal(targets.get("ja-google").scrolls, 2);

  // Unknown fragments are left intact instead of redirecting to the top.
  location.hash = "#en-unknown";
  for (const listener of listeners.get("hashchange")) listener();
  assert.equal(location.hash, "#en-unknown");
});
