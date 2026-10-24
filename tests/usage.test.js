import test from "node:test";
import assert from "node:assert/strict";
import { createUsage, costText, usageTotals } from "../frontend/src/usage.js";
import { api } from "../frontend/src/api.js";
import { t } from "../frontend/src/i18n.js";

class Element {
  children = []; listeners = {}; textContent = ""; value = "";
  append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
  remove() { this.parent.children.splice(this.parent.children.indexOf(this), 1); }
  get firstChild() { return this.children[0]; }
  setAttribute(key, value) { this[key] = value; }
  addEventListener(key, fn) { this.listeners[key] = fn; }
}
const walk = node => [node, ...node.children.flatMap(walk)];
const text = node => walk(node).map(node => node.textContent).join(" ");

test("costs distinguish free, unknown, and incomplete totals", () => {
  assert.equal(costText({ calls: 1, reportedUsd: 0, unpriced: 0 }), "US$0.0000");
  assert.equal(costText({ calls: 1, unpriced: 1 }), t("未算出（{0}件）", 1));
  assert.equal(costText({ calls: 2, unpriced: 1, reportedUsd: 0.02 }), `US$0.0200 + ${t("未算出（{0}件）", 1)}`);
  assert.deepEqual(usageTotals([{ calls: 1, estimatedUsd: 0.01, searches: 2 }, { calls: 1, reportedUsd: 0.02, unpriced: 1 }]), {
    calls: 2, failures: 0, estimatedUsd: 0.01, reportedUsd: 0.02, unpriced: 1, searches: 2, searchFailures: 0,
  });
});

test("persona cards request only that persona and overall Other includes embeddings", async () => {
  const saved = { Node: globalThis.Node, document: globalThis.document, usage: api.usage };
  globalThis.Node = Element;
  globalThis.document = { createElement: tag => new Element(tag), createTextNode: value => Object.assign(new Element("text"), { textContent: value }) };
  try {
    const requests = [];
    api.usage = async (month, persona) => {
      requests.push([month, persona]);
      return { usage: { month: "2026-10", rows: persona ? [{ category: "scheduled", component: "persona", calls: 1, reportedUsd: 0.02, searches: 2 }] : [
        { component: "embedding", calls: 1, estimatedUsd: 0.01 },
        { component: "other", calls: 1, estimatedUsd: 0.03 },
        { component: "persona", persona: "gourmet", category: "reply", calls: 1, estimatedUsd: 0.05 },
        { component: "persona", persona: "agent", category: "reply", calls: 1, estimatedUsd: 0.07 },
      ] } };
    };
    const persona = createUsage({ persona: "gourmet" });
    assert.equal(requests.length, 0, "closed cards must not fetch usage");
    await persona.load();
    const now = new Date();
    assert.deepEqual(requests[0], [`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`, "gourmet"]);
    assert.match(text(persona.element), /US\$0.0200/);
    assert.ok(text(persona.element).includes(t("定期実行")));
    const overall = createUsage();
    await overall.load();
    const table = walk(overall.element).find(node => node.className === "usage-table");
    assert.ok(text(table).includes("US$0.0400"));
    assert.ok(!text(table).includes("Embedding"), "embedding is grouped into Other");
    assert.ok(text(overall.element).includes(t("担当合計")));
    // Questions to @ai have no persona card; they get their own row, apart from the personas.
    const cells = (label) => walk(table).find(node => node.children[0]?.textContent === t(label));
    assert.ok(text(cells("担当合計")).includes("US$0.0500"));
    assert.ok(text(cells("@ai")).includes("US$0.0700"));
  } finally { Object.assign(globalThis, { Node: saved.Node, document: saved.document }); api.usage = saved.usage; }
});
