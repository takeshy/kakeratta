import test from "node:test";
import assert from "node:assert/strict";
import { renderMarkdown, looksLikeHtml, renderHtmlDocument, spokenText } from "../frontend/src/markdown.js";

// The same minimal DOM the other view tests use: element creation, text
// nodes, and nothing else. Rendering must not depend on anything a browser
// adds.
class Element {
  children = []; attributes = {}; textContent = ""; className = "";
  constructor(tag) { this.tagName = tag.toUpperCase(); }
  setAttribute(key, value) { this.attributes[key] = value; }
  append(...nodes) { this.children.push(...nodes); }
  classList = {
    contains: (name) => this.className.split(" ").includes(name),
    toggle: (name, on) => {
      const names = new Set(this.className.split(" ").filter(Boolean));
      if (on) names.add(name); else names.delete(name);
      this.className = [...names].join(" ");
    },
  };
}
const all = (node) => [node, ...node.children.flatMap(all)];
const tags = (root, name) => all(root).filter((node) => node.tagName === name);
const text = (node) => all(node).map((part) => part.textContent).join("");

function withDOM(run) {
  return (t) => {
    globalThis.Node = Element;
    globalThis.document = {
      createElement: (tag) => new Element(tag),
      createTextNode: (text) => Object.assign(new Element("text"), { textContent: text }),
    };
    t.after(() => { delete globalThis.Node; delete globalThis.document; });
    run();
  };
}

test("block markdown reads as itself: headings, lists, tasks, tables, quotes, fences", withDOM(() => {
  const view = renderMarkdown("# Heading\n- one\n- [x] done\n- [ ] todo\n1. first\n| a | b |\n| --- | --- |\n| 1 | 2 |\n> quoted\n> second\n```\nlet x = 1\n```");
  assert.equal(view.className, "markdown-body");
  const [heading] = tags(view, "H4");
  assert.equal(text(heading), "Heading");
  const lists = tags(view, "UL");
  assert.equal(lists.length, 1, "one bullet list");
  const items = tags(view, "LI");
  assert.deepEqual(items.map(item => text(item)), ["one", "☑ done", "☐ todo", "first"]);
  assert.ok(!items[0].classList.contains("task"), "a normal item keeps its bullet beside task items");
  assert.ok(items[1].classList.contains("task"), "only task items replace their bullet with a marker");
  const [ordered] = tags(view, "OL");
  assert.equal(text(ordered.children[0]), "first");
  const cells = tags(view, "TH").map(cell => text(cell));
  assert.deepEqual(cells, ["a", "b"]);
  assert.deepEqual(tags(view, "TD").map(cell => text(cell)), ["1", "2"]);
  const [quote] = tags(view, "BLOCKQUOTE");
  assert.equal(quote.children.length, 3, "two quoted lines join as one quotation with a break");
  const [fence] = tags(view, "PRE");
  assert.equal(fence.children[0].textContent, "let x = 1");
  // Consecutive prose lines are one paragraph with breaks, not one per line.
  const view2 = renderMarkdown("first\nsecond\n\nthird");
  const paragraphs = tags(view2, "P");
  assert.equal(paragraphs.length, 2);
  assert.equal(paragraphs[0].children.length, 3, "text, br, text");
}));

test("inline markdown renders and never runs: links, bare URLs, inert schemes", withDOM(() => {
  const view = renderMarkdown("**bold** ~~gone~~ `code` [ok](https://example.com/x) [bad](javascript:alert(1)) https://example.com/y");
  assert.equal(tags(view, "STRONG")[0].textContent, "bold");
  assert.equal(tags(view, "DEL")[0].textContent, "gone");
  assert.equal(tags(view, "CODE")[0].textContent, "code");
  const links = tags(view, "A");
  assert.deepEqual(links.map((link) => link.textContent), ["ok", "https://example.com/y"]);
  assert.ok(links.every((link) => link.attributes.target === "_blank"));
  const [inert] = tags(view, "SPAN");
  assert.equal(inert.textContent, "bad");
  assert.equal(tags(view, "A").length, 2, "the javascript: link is not a link");
}));

test("a quoted [!type] first line reads as a callout, with gemihub's aliases and folding", withDOM(() => {
  const view = renderMarkdown("> [!warning] Watch out\n> body line");
  const [callout] = tags(view, "DIV").filter((node) => node.classList.contains("markdown-callout"));
  assert.equal(callout.attributes["data-type"], "warning");
  assert.equal(all(callout).find((node) => node.tagName === "SPAN" && node.textContent === "!").textContent, "!");
  assert.match(all(callout).map((node) => node.textContent).join(""), /Watch out/);
  assert.match(all(callout).map((node) => node.textContent).join(""), /body line/);

  const alias = renderMarkdown("> [!hint]\n> try this");
  const [tip] = tags(alias, "DIV").filter((node) => node.classList.contains("markdown-callout"));
  assert.equal(tip.attributes["data-type"], "tip");
  assert.match(all(tip).map((node) => node.textContent).join(""), /Hint/);

  const closed = renderMarkdown("> [!note]- Title only\n> hidden body");
  const [folded] = tags(closed, "DIV").filter((node) => node.classList.contains("markdown-callout"));
  assert.ok(!all(folded).some((node) => node.tagName === "DIV" && node.classList.contains("markdown-callout-body")), "a closed callout shows its head only");

  const unknown = renderMarkdown("> [!wharrgarbl] Whatever");
  assert.equal(tags(unknown, "DIV").filter((node) => node.classList.contains("markdown-callout"))[0].attributes["data-type"], "note");
}));

test("reading aloud says the words, not the markdown punctuation", () => {
  assert.equal(spokenText("# Heading\n- [x] done\n- item\n1. first\n> quoted\n**bold** ~~gone~~ `code` [ok](https://example.com/x)\n```\nlet x = 1\n```\nPlain"),
    "Heading\ndone\nitem\nfirst\nquoted\nbold gone code ok\nlet x = 1\nPlain");
  assert.equal(spokenText("何もない"), "何もない");
});

test("only whole HTML documents take the framed path; prose and tag mentions do not", withDOM(() => {
  for (const document of ["<!DOCTYPE html><html>", "<html>", "<div>x</div>", "<p>hi", "<TABLE>", "<img src=x>"]) {
    assert.ok(looksLikeHtml(document), document);
  }
  for (const plain of ["", "plain **text**", "# heading", "1 < 2 and 3 > 1", "<3 hearts", "<hoge>不明", "a < b"]) {
    assert.ok(!looksLikeHtml(plain), plain);
  }
  const frame = renderHtmlDocument("<!DOCTYPE html><html><body>x</body></html>");
  assert.equal(frame.tagName, "IFRAME");
  assert.equal(frame.attributes.sandbox, "allow-popups");
  assert.equal(frame.srcdoc ?? frame.attributes.srcdoc, '<base target="_blank"><!DOCTYPE html><html><body>x</body></html>');
  assert.ok(Object.keys(frame.attributes).includes("title"));
}));
