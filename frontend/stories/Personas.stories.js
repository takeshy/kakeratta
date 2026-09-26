import { showPersonaBuilder } from "../src/persona-builder.js";
import { getLanguage } from "../src/i18n.js";
import { createPersonas } from "../src/personas.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { memory, nobody, personas, tools } from "./fixtures.js";

export default { title: "Personas", parameters: { layout: "fullscreen" } };

// The dialog the panel opens lives on <body>, outside the story root, so a
// story starts by taking down the one the previous story left.
function panelOf(rows, after, { mcp } = {}) {
  for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
  serve({ personas: rows, tools, memory, ...(mcp ? { mcp } : {}) });
  const panel = createPersonas({ onChange: () => {} });
  void panel.load().then(() => after?.(panel.element));
  return sheet("担当", panel.element);
}

export const AllPresent = { name: "4体とも有効", render: () => panelOf(personas) };

export const OneOn = {
  name: "1体だけ有効",
  render: () => panelOf(personas.map((row) => ({ ...row, active: row.id === "book" }))),
};

// shiori plans through the connected external agent; the others plan in-process.
export const ExternalPlanner = {
  name: "考える役を外部エージェントにした",
  render: () => panelOf(personas.map((row) => row.id === "book" ? { ...row, planner: "external" } : row),
    (element) => element.querySelector('[data-id="book"]')?.scrollIntoView({ block: "start" }),
    { mcp: { configured: true, path: "/api/mcp/users/sample" } }),
};

export const Nobody = { name: "誰もいない", render: () => panelOf(nobody) };

export const WithAdded = {
  name: "追加した担当がいる（削除できる）",
  render: () => panelOf([
    ...personas,
    { id: "wine", name: "ぶどう", icon: "🍷", builtin: false, active: true, wakes: 2,
      domain: "ワイン。銘柄、産地、ラベルの写真、飲んだ記録。", tools: ["web_search"] },
  ]),
};

export const Memory = {
  name: "メモを見る",
  render: () => panelOf(personas, (element) => {
    [...element.querySelectorAll("button")].find((button) => /覚えて|remember/i.test(button.textContent))?.click();
  }),
};

export const Install = {
  name: "追加フォームを開いた",
  render: () => panelOf(personas, (element) => {
    element.querySelector("details.install").open = true;
  }),
};

// Real creation UI with a sample AI response, for reproducible documentation.
export const AICreateStart = {
  name: "AIと担当を作る・入力",
  render: () => panelOf(personas, () => showPersonaBuilder({})),
};

export const AICreateDraft = {
  name: "AIと担当を作る・完成案",
  render: () => panelOf(personas, () => {
    const ja = getLanguage() === "ja";
    const draft = {
      id: "wine", name: ja ? "ぶどう" : "budou", icon: "🍷", tools: ["web_search"],
      domain: ja ? "ラベルの写真から、ワインの銘柄・産地・ブドウ品種を解説し、お店で買ったり注文したりできるようにする。" : "Identify wines from label photos and explain the producer, region, and grapes so you can buy or order them.",
      memory: ja ? "型は Wine（銘柄。産地・品種・感想）と Preference（好みの傾向。同じ好みが3回出たら統合）の2つ。" : "Two kinds: Wine (producer, region, grapes, impressions) and Preference (a taste that has come up three times).",
      tone: ja ? "気軽に話せる友達のように、短く親しみやすく。" : "Friendly and relaxed, with short replies.",
      instructions: ja ? "ラベルが届いたら、ワインショップやお店で伝わる名前と、ブドウ品種・味わいを短く解説します。飲んだワインと感想も覚えます。" : "When a label arrives, give the name a shop or restaurant would recognize, with its grapes and style. Remember the wines you try and what you thought.",
    };
    serve({ personas, tools, memory, personaDraft: {
      message: ja ? "ワイン担当「ぶどう」の案を作りました。使ってからAIで修正できます。" : "Here is budou, your wine persona. Try it first and refine it later with AI.",
      placeholder: ja ? "例：似た味わいで手頃なワインも教えてほしい。" : "Example: Also suggest similar wines at a lower price.",
      draft,
    }});
    showPersonaBuilder({});
    const dialog = document.querySelector(".persona-builder");
    dialog.querySelector("textarea").value = ja
      ? "美味しかったワインのラベルを送るので、銘柄やブドウ品種を教えてほしい。お店で注文したり買ったりできるように。"
      : "When I send a photo of a wine label I liked, tell me the wine and its grape varieties so I can buy or order it.";
    dialog.querySelector("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  }),
};

export const WineMemory = {
  name: "ワイン担当の記憶（サンプル）",
  render: () => {
    for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
    const ja = getLanguage() === "ja";
    const wine = { id: "wine", name: ja ? "ワイン担当" : "Wine", icon: "🍷", active: true,
      domain: ja ? "飲んだワインと、好きだった理由を覚えます。" : "Remembers the wines you tried and why you liked them.", tools: [], wakes: 4 };
    const sources = `<!-- kakeratta:note-sources -->\n# 記憶を更新した投稿\n\n* [${ja ? "9月12日 — レストランでピノ・ノワールを気に入った" : "Sep 12 — liked Pinot Noir at a restaurant"}](/?entry=sample-pinot)\n* [${ja ? "9月13日 — ガメイを「飲みやすい」と話した" : "Sep 13 — described Gamay as easy to drink"}](/?entry=sample-gamay)\n<!-- /kakeratta:note-sources -->`;
    const notes = [
      { path: "preferences/light-reds.md", text: `---\ntype: Preference\ntitle: ${ja ? "軽めの赤ワインが好み" : "Prefers lighter red wines"}\ntags: [${ja ? "赤ワイン, ピノ・ノワール, ガメイ" : "red wine, Pinot Noir, Gamay"}]\n---\n${ja ? "果実味があり、タンニンが穏やかな赤ワインを気に入っている。" : "Enjoys fruity reds with gentle tannins."}\n\n${sources}` },
      { path: "wines/domaine-x.md", text: `---\ntype: Wine\ntitle: Domaine X Pinot Noir\ntags: [${ja ? "赤ワイン, ピノ・ノワール, ブルゴーニュ" : "red wine, Pinot Noir, Burgundy"}]\ndescription: ${ja ? "レストランで飲んだ、軽やかな赤ワイン。" : "A light red tried at a restaurant."}\n---\n# ${ja ? "感想" : "Tasting notes"}\n- ${ja ? "「重くなくて、もう一杯飲みたくなる」" : '“Not heavy; makes me want another glass.”'}\n- [${ja ? "軽めの赤ワインが好み" : "Prefers lighter red wines"}](/preferences/light-reds.md)\n\n<!-- kakeratta:note-sources -->\n# 記憶を更新した投稿\n\n* [${ja ? "9月12日 — レストランでの記録" : "Sep 12 — restaurant note"}](/?entry=sample-pinot)\n<!-- /kakeratta:note-sources -->` },
    ];
    serve({ personas: [wine], tools, memory: { wine: { notes } } });
    const panel = createPersonas({ onChange: () => {} });
    void panel.load().then(() => {
      [...panel.element.querySelectorAll("button")].find(button => /覚えて|remember/i.test(button.textContent))?.click();
    });
    return sheet("担当", panel.element);
  },
};
