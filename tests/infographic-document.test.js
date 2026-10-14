import test from "node:test";
import assert from "node:assert/strict";
import { imageSource, infographicDocument } from "../frontend/src/infographic-document.js";

// A DOM just rich enough to serialise what the infographic renderer builds.
class Element {
  children = []; attributes = {}; textContent = ""; className = "";
  constructor(tag) { this.tagName = tag.toUpperCase(); }
  setAttribute(key, value) { this.attributes[key] = value; }
  append(...nodes) { this.children.push(...nodes); }
  get outerHTML() {
    if (this.tagName === "#TEXT") return this.textContent;
    const tag = this.tagName.toLowerCase();
    return `<${tag} class="${this.className}">${this.textContent}${this.children.map(child => child.outerHTML).join("")}</${tag}>`;
  }
}

function withDOM(sheets, run) {
  return (t) => {
    globalThis.Node = Element;
    globalThis.document = {
      styleSheets: sheets,
      documentElement: { lang: "en" },
      createElement: (tag) => new Element(tag),
      createElementNS: (_, tag) => new Element(tag),
      createTextNode: (text) => Object.assign(new Element("#text"), { textContent: text }),
    };
    t.after(() => { delete globalThis.Node; delete globalThis.document; });
    run();
  };
}

const sheets = [
  { cssRules: [
    { cssText: ":root { --bg: #faf8f4; }" },
    { cssText: "@media (prefers-color-scheme: dark) { :root { --bg: #161513; } }", media: { mediaText: "(prefers-color-scheme: dark)" }, cssRules: [] },
    { cssText: ".infographic-section { border-top: 4px solid var(--tone); }" },
    { cssText: "@import url(https://example.com/x.css);" },
  ] },
  { get cssRules() { throw new Error("another origin"); } },
];

test("a laid-out infographic becomes a standalone light-theme document", withDOM(sheets, () => {
  const source = imageSource({ speaker: "infographic", text: "# 📊 売上\n## 📈 推移\n- 上昇" });
  assert.match(source, /^<!doctype html><html lang="en">/);
  assert.match(source, /--bg: #faf8f4/);
  assert.doesNotMatch(source, /#161513/, "the dark theme is left out");
  assert.doesNotMatch(source, /@import/);
  assert.match(source, /class="said said-markdown said-infographic"/);
  assert.match(source, /class="infographic-hero"/);
  assert.match(source, /売上/);
  assert.match(infographicDocument("# x", "a{}</style><script>"), /a\{\}<\\\/style><script>/, "CSS cannot close its style element");
}));

test("only HTML answers and the infographic's or agent's laid-out Markdown are published as pictures", withDOM([], () => {
  const html = "<!DOCTYPE html><html><body>図</body></html>";
  assert.equal(imageSource({ speaker: "gourmet", text: html }), html);
  assert.equal(imageSource({ speaker: "user", text: html }), null);
  assert.equal(imageSource({ speaker: "gourmet", text: "# 見出し\n本文" }), null);
  assert.equal(imageSource({ speaker: "infographic", text: "本文を貼ってください。" }), null);
  assert.match(imageSource({ speaker: "agent", text: "# 🔗 連携\n## 📨 依頼\n- 返信" }), /class="infographic-hero"/);
  assert.equal(imageSource({ speaker: "agent", text: "確認させてください。" }), null);
}));
