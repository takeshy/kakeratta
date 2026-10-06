import test from "node:test";
import assert from "node:assert/strict";
import { renderMarkdown, spokenText } from "../frontend/src/markdown.js";

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

test("labelled code fences highlight safely and Mermaid fences become diagrams", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  const view = renderMarkdown("```js\nconst answer = 42; // safe\n```\n```mermaid\nflowchart LR\nA[Question] --> B[Answer]\n```");
  const [code] = tags(view, "CODE");
  assert.equal(code.className, "language-js");
  assert.deepEqual(tags(code, "SPAN").map(part => part.className), ["token-keyword", "token-number", "token-comment"]);
  assert.equal(tags(view, "SVG").length, 1, "the Mermaid source is replaced by an SVG");
  assert.ok(tags(view, "TEXT").some(node => node.textContent === "Question"));
  assert.equal(tags(view, "SCRIPT").length, 0, "labels can never create executable markup");
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

test("unsupported flowchart statements preserve the entire source instead of a partial diagram", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  for (const statement of ["A --> A", "A ~~> B", "A[unclosed --> B"]) {
    const source = `flowchart LR\nX[Existing] --> Y[Valid]\n${statement}`;
    const view = renderMarkdown(`\`\`\`mermaid\n${source}\n\`\`\``);
    assert.equal(tags(view, "SVG").length, 0, statement);
    assert.equal(text(tags(view, "PRE")[0]), source, statement);
  }
}));

test("flowchart arrow tips stop outside node boundaries in both axes and edge directions", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  for (const direction of ["LR", "TD"]) {
    for (const edge of ["A --> B", "B --> A"]) {
      const view = renderMarkdown(`\`\`\`mermaid\nflowchart ${direction}\nA[Short]\nB[A longer label]\n${edge}\n\`\`\``);
      const [line] = tags(view, "LINE");
      const boxes = tags(view, "RECT");
      const target = boxes[edge.startsWith("A") ? 1 : 0].attributes;
      const x = Number(line.attributes.x2), y = Number(line.attributes.y2);
      const horizontal = direction === "LR";
      const start = Number(target[horizontal ? "x" : "y"]);
      const end = start + Number(target[horizontal ? "width" : "height"]);
      const tip = horizontal ? x : y;
      assert.ok(tip < start || tip > end, `${direction}: ${edge} arrowhead must be outside target`);
      assert.equal(Math.min(Math.abs(tip - start), Math.abs(tip - end)), 1);
      assert.match(line.attributes["marker-end"], /^url\(#arrow-/);
      assert.equal(tags(view, "MARKER")[0].attributes.refX, "10", "marker tip is aligned with the endpoint");
    }
  }
  const undirected = renderMarkdown("```mermaid\nflowchart LR\nA --- B\n```");
  assert.equal(tags(undirected, "LINE")[0].attributes["marker-end"], undefined);
}));

test("sequence aliases are displayed while message endpoints retain participant IDs", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  const view = renderMarkdown("```mermaid\nsequenceDiagram\nparticipant A as Alice\nactor B as Bob\nA->>B: Hello\nB->>C: Forward\n```");
  assert.equal(tags(view, "SVG").length, 1);
  const labels = tags(view, "TEXT").filter(node => node.attributes.class === "mermaid-node-label");
  assert.deepEqual(labels.map(node => node.textContent), ["Alice", "Bob", "C"]);
  const edges = tags(view, "LINE").filter(node => node.attributes.class === "mermaid-edge");
  assert.deepEqual(edges.map(node => [node.attributes.x1, node.attributes.x2]), [[90, 270], [270, 450]]);
}));

test("flowcharts read the syntax models write: multi-line labels, pipe labels, chains, fan-out and subgraphs", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  const source = [
    "flowchart TD",
    "  subgraph Morning[朝]",
    "    A[🔬 Research",
    "    Cursor+Hex] -->|旧| B{判断}",
    "  end",
    "  B -- 新 --> C[完了<br>送信] --> D(終わり)",
    "  C & D -.->|連携| E[(記録)]",
    "  E ==> F",
    "  style A fill:#f9f",
  ].join("\n");
  const view = renderMarkdown(`\`\`\`mermaid\n${source}\n\`\`\``);
  assert.equal(tags(view, "SVG").length, 1);
  const nodeLabels = tags(view, "TEXT").filter(node => node.attributes.class === "mermaid-node-label");
  assert.deepEqual(nodeLabels.map(text), ["🔬 ResearchCursor+Hex", "判断", "完了送信", "終わり", "記録", "F"]);
  assert.deepEqual(tags(nodeLabels[0], "TSPAN").map(node => node.textContent), ["🔬 Research", "Cursor+Hex"]);
  const edges = tags(view, "LINE").filter(node => /mermaid-edge/.test(node.attributes.class));
  assert.equal(edges.length, 6);
  assert.equal(edges.filter(node => /dotted/.test(node.attributes.class)).length, 2);
  assert.equal(edges.filter(node => /thick/.test(node.attributes.class)).length, 1);
  const edgeLabels = tags(view, "TEXT").filter(node => node.attributes.class === "mermaid-edge-label").map(text);
  assert.deepEqual(edgeLabels.sort(), ["新", "旧", "連携", "連携"].sort());
  assert.equal(tags(view, "RECT").filter(node => /mermaid-decision/.test(node.attributes.class)).length, 1);
}));

test("flowcharts are layered, wide layers wrap and wide left-to-right charts turn top-down", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  const centers = (view) => Object.fromEntries(tags(view, "TEXT").filter(node => node.attributes.class === "mermaid-node-label")
    .map(node => [text(node), Number(node.attributes.y)]));
  const branch = renderMarkdown("```mermaid\nflowchart TD\nA --> B\nA --> C\nB --> D\nC --> D\n```");
  const y = centers(branch);
  assert.ok(y.A < y.B && y.B === y.C && y.C < y.D, "siblings share a layer");
  const fan = renderMarkdown("```mermaid\nflowchart TD\nR --> A1[一番目の項目]\nR --> A2[二番目の項目]\nR --> A3[三番目の項目]\nR --> A4[四番目の項目]\nR --> A5[五番目の項目]\n```");
  const [svgNode] = tags(fan, "SVG");
  assert.ok(Number(svgNode.attributes.width) <= 640, "a wide layer wraps");
  const chain = renderMarkdown("```mermaid\nflowchart LR\nA[一] --> B[二] --> C[三] --> D[四] --> E[五]\n```");
  const yc = centers(chain);
  assert.ok(yc.一 < yc.二 && yc.二 < yc.五, "a long left-to-right chain is drawn top-down");
}));

test("sequence diagrams read notes, self messages, continued lines, spaced ids and control blocks", withDOM(() => {
  document.createElementNS = (_namespace, tag) => new Element(tag);
  const source = [
    "sequenceDiagram",
    "participant H as 👤 Human",
    "Note over H: 夜間に準備",
    "loop 毎朝",
    "H->>B: 全件を",
    "3システム照合",
    "B-->>H: 結果",
    "H->>H: 判断",
    "end",
    "B->>🔗 John Ford: 送信",
  ].join("\n");
  const view = renderMarkdown(`\`\`\`mermaid\n${source}\n\`\`\``);
  assert.equal(tags(view, "SVG").length, 1);
  const actors = tags(view, "TEXT").filter(node => node.attributes.class === "mermaid-node-label").map(text);
  assert.deepEqual(actors, ["👤 Human", "B", "🔗 John Ford"]);
  const labels = tags(view, "TEXT").filter(node => node.attributes.class === "mermaid-edge-label");
  assert.deepEqual(labels.map(text), ["夜間に準備", "全件を3システム照合", "結果", "判断", "送信"]);
  assert.equal(tags(view, "RECT").filter(node => node.attributes.class === "mermaid-note").length, 1);
  assert.equal(tags(view, "LINE").filter(node => /dotted/.test(node.attributes.class)).length, 1);
}));
