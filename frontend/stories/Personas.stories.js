import { showPersonaBuilder } from "../src/persona-builder.js";
import { getLanguage } from "../src/i18n.js";
import { createPersonas } from "../src/personas.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { memory, nobody, person, personCalendar, personWords, personas, tools } from "./fixtures.js";

export default { title: "Personas", parameters: { layout: "fullscreen" } };

// The dialog the panel opens lives on <body>, outside the story root, so a
// story starts by taking down the one the previous story left.
// The tab and the order a viewer chose are kept in the browser; a story starts
// from the defaults, or from the tab it names.
function panelOf(rows, after, { mcp, tab } = {}) {
  for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
  try {
    localStorage.removeItem("kakeratta.personas.tab");
    localStorage.removeItem("kakeratta.personas.order");
  } catch { /* nothing kept */ }
  serve({ personas: rows, tools, memory, ...(mcp ? { mcp } : {}) });
  const panel = createPersonas({ onChange: () => {} });
  void panel.load().then(() => {
    if (tab) [...panel.element.querySelectorAll(".persona-tab")][tab === "person" ? 0 : 1]?.click();
    after?.(panel.element);
  });
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

export const AICreatePersonStart = {
  name: "AIと人物の担当を作る・入力",
  render: () => panelOf(personas, () => showPersonaBuilder({ kind: "person" })),
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
    dialog.querySelector("form textarea").value = ja
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

// ---- a person ---------------------------------------------------------------

const withPerson = () => [...personas, person()];
const personCard = (element) => element.querySelector('[data-id="sato"]');

// A person's card shows the names and keywords that reach it, since nothing
// else does.
export const PersonCard = {
  name: "人物の担当がいる",
  render: () => panelOf([personCalendar(), ...personas], null, { tab: "person" }),
};

// People on a tab of their own, found by any of their names or keywords, and
// in name order by the reading of their names.
const people = () => {
  const ja = getLanguage() === "ja";
  return [
    person(),
    { ...person(), id: "aoki", dailyCheckIn: { enabled: true, talk: "share", every: "monthly", on: 12 }, basicInfo: "", name: ja ? "青木先生" : "Ms Aoki", icon: "🎹", domain: ja ? "子どものピアノの先生。" : "Our child's piano teacher.", aliases: ja ? ["青木", "あおき", "先生"] : ["Aoki", "teacher"], keywords: ja ? ["ピアノ教室", "発表会"] : ["piano lesson", "recital"] },
    { ...person(), id: "mother", basicInfo: "", name: ja ? "母" : "Mum", icon: "👩", domain: ja ? "母。" : "My mother.", aliases: ja ? ["お母さん", "おかあさん", "おかん"] : ["Mother", "Mom"], keywords: ja ? ["実家"] : ["my parents' house"] },
  ];
};

export const PeopleByName = {
  name: "人物のタブ・名前順",
  render: () => panelOf([...personas, ...people()], (element) => {
    const order = element.querySelector(".persona-order");
    order.value = "name";
    order.dispatchEvent(new Event("change"));
  }, { tab: "person" }),
};

export const PeopleSearch = {
  name: "人物のタブ・検索",
  render: () => panelOf([...personas, ...people()], (element) => {
    const search = element.querySelector(".persona-search");
    search.value = getLanguage() === "ja" ? "ぴあの" : "piano";
    search.dispatchEvent(new Event("input"));
  }, { tab: "person" }),
};

export const PersonEdit = {
  name: "人物の担当を修正",
  render: () => panelOf(withPerson(), (element) => {
    element.querySelectorAll(".persona-tab")[0]?.click();
    [...personCard(element).querySelectorAll("button")].find((button) => /^(修正|Edit)$/.test(button.textContent))?.click();
  }),
};

export const PersonCreateDraft = {
  name: "AIと担当を作る・人物の完成案",
  render: () => panelOf(personas, () => {
    const w = personWords();
    const { active, wakes, planner, dailyCheckIn, builtin, ...draft } = person();
    serve({ personas, tools, memory, personaDraft: { message: w.builderMessage, placeholder: "", draft } });
    showPersonaBuilder({});
    const dialog = document.querySelector(".persona-builder");
    dialog.querySelector("form textarea").value = w.builderRequest;
    dialog.querySelector("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  }),
};
