import test from "node:test";
import assert from "node:assert/strict";
import { isInfographic, laidOutAsInfographic, renderInfographic } from "../frontend/src/infographic.js";

// The minimal DOM the other view tests use.
class Element {
  children = []; attributes = {}; textContent = ""; className = "";
  constructor(tag) { this.tagName = tag.toUpperCase(); }
  setAttribute(key, value) { this.attributes[key] = value; }
  append(...nodes) { this.children.push(...nodes); }
}
const all = (node) => [node, ...node.children.flatMap(all)];
const byClass = (root, name) => all(root).filter((node) => node.className.split(" ").includes(name));
const text = (node) => all(node).map((part) => part.textContent).join("");

function withDOM(run) {
  return (t) => {
    globalThis.Node = Element;
    globalThis.document = {
      createElement: (tag) => new Element(tag),
      createElementNS: (_, tag) => new Element(tag),
      createTextNode: (value) => Object.assign(new Element("text"), { textContent: value }),
    };
    t.after(() => { delete globalThis.Node; delete globalThis.document; });
    run();
  };
}

const answer = [
  "# 🍽️ 昼食の比較",
  "駅前の2店を比べます。",
  "",
  "## 📍 場所",
  "- 🚶 A店: 3分",
  "- 🚃 B店: 10分",
  "### 🗺️ 補足",
  "どちらも駅の北側です。",
  "",
  "## 🔀 選び方",
  "```mermaid",
  "flowchart TD",
  "A[⏱️ 急ぐ?] --> B[A店]",
  "```",
  "```bash",
  "# a comment, not a heading",
  "```",
].join("\n");

test("the title becomes a banner and each top section a toned card", withDOM(() => {
  const view = renderInfographic(answer);
  assert.equal(view.className, "infographic");
  const [hero] = byClass(view, "infographic-hero");
  assert.equal(text(byClass(hero, "infographic-title")[0]), "🍽️昼食の比較");
  assert.equal(byClass(hero, "infographic-emoji")[0].textContent, "🍽️");
  assert.match(text(hero), /駅前の2店を比べます。/);
  const sections = byClass(view, "infographic-section");
  assert.deepEqual(sections.map((section) => section.attributes["data-tone"]), ["0", "1"]);
  assert.deepEqual(sections.map((section) => byClass(section, "infographic-emoji")[0].textContent), ["📍", "🔀"]);
  // Deeper headings, lists, diagrams and code stay inside their card.
  assert.match(text(sections[0]), /補足/);
  assert.ok(all(sections[0]).some((node) => node.tagName === "UL"));
  assert.equal(byClass(sections[1], "markdown-mermaid").length, 1);
  assert.match(text(sections[1]), /# a comment, not a heading/);
}));

test("without a single title, the shallowest headings are the cards and nothing is dropped", withDOM(() => {
  const view = renderInfographic("前置き\n\n## ✅ 利点\n速い\n# 📌 まとめ\n安い");
  assert.equal(byClass(view, "infographic-hero").length, 0);
  const [lead] = byClass(view, "infographic-lead");
  assert.match(text(lead), /前置き/);
  assert.match(text(lead), /利点/);
  assert.match(text(lead), /速い/);
  const sections = byClass(view, "infographic-section");
  assert.equal(sections.length, 1);
  assert.match(text(sections[0]), /まとめ/);
  assert.match(text(sections[0]), /安い/);
}));

test("plain prose is not laid out as an infographic", () => {
  assert.equal(isInfographic("元の記事の本文を貼ってください。"), false);
  assert.equal(isInfographic("```\n# code\n```"), false);
  assert.equal(isInfographic(answer), true);
});

test("the infographic persona and the external agent are laid out; other speakers are not", () => {
  assert.equal(laidOutAsInfographic("infographic", answer), true);
  assert.equal(laidOutAsInfographic("agent", answer), true);
  assert.equal(laidOutAsInfographic("agent", "確認させてください。"), false);
  assert.equal(laidOutAsInfographic("gourmet", answer), false);
  assert.equal(laidOutAsInfographic("user", answer), false);
});
