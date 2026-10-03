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
    if (tab) [...panel.element.querySelectorAll(".persona-tab")][tab === "person" ? 0 : tab === "schedules" ? 2 : 1]?.click();
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
      memory: ja ? "分野は Wine（飲んだ銘柄と感想）と Preference（好みの傾向。重なったらまとめる）の2つ。記録のタグには産地・品種・色を付ける。" : "Fields: Wine (wines you tried and what you thought) and Preference (your tastes, merged as they repeat). Tag records with region, grape, and colour.",
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
    const notes = [
      { path: "Preference.md", text: ja
        ? "# Preference\n\n## まとめ（2026-09-14）\n\n果実味があり、タンニンが穏やかな軽めの赤が好き。重い赤は「もう一杯」にならない。\n\n## いまの事実\n\n- [f_r5d2kp] 軽めの赤が好き from:sample-pinot,sample-gamay since:2026-09-13\n\n## 以前のこと\n\n- [f_h8j3mn] しっかりした赤が好き from:sample-old since:2026-08-02 until:2026-09-13 by:f_r5d2kp\n"
        : "# Preference\n\n## まとめ（2026-09-14）\n\nLikes lighter, fruity reds with gentle tannins; a heavy red does not make them want another glass.\n\n## いまの事実\n\n- [f_r5d2kp] Likes lighter reds from:sample-pinot,sample-gamay since:2026-09-13\n\n## 以前のこと\n\n- [f_h8j3mn] Likes full-bodied reds from:sample-old since:2026-08-02 until:2026-09-13 by:f_r5d2kp\n" },
      { path: "Wine.md", text: ja
        ? "# Wine\n\n## いまの事実\n\n- [f_t6v9wq] Domaine X Pinot Noir：レストランで。「重くなくて、もう一杯飲みたくなる」 from:sample-pinot since:2026-09-12\n- [f_y2z4ab] ボージョレのガメイ：「飲みやすい」 from:sample-gamay since:2026-09-13\n"
        : "# Wine\n\n## いまの事実\n\n- [f_t6v9wq] Domaine X Pinot Noir: at a restaurant. “Not heavy; makes me want another glass.” from:sample-pinot since:2026-09-12\n- [f_y2z4ab] A Beaujolais Gamay: “easy to drink.” from:sample-gamay since:2026-09-13\n" },
      { path: "records/2026-09.md", text: ja
        ? "# 2026-09 のかけら\n\n- 2026-09-12 20:31 [sample-pinot](/?entry=sample-pinot) レストランで飲んだピノ・ノワールを気に入った #赤ワイン #ピノ・ノワール #ブルゴーニュ\n- 2026-09-13 19:02 [sample-gamay](/?entry=sample-gamay) ガメイを飲みやすいと言った #赤ワイン #ガメイ\n- 2026-09-20 18:15 [sample-shop](/?entry=sample-shop) 恵比寿のワインバーに行ってみたいと言った #恵比寿 #ワインバー review:2026-10-11/21d\n"
        : "# 2026-09\n\n- 2026-09-12 20:31 [sample-pinot](/?entry=sample-pinot) Liked a Pinot Noir at a restaurant #red #PinotNoir #Burgundy\n- 2026-09-13 19:02 [sample-gamay](/?entry=sample-gamay) Found Gamay easy to drink #red #Gamay\n- 2026-09-20 18:15 [sample-shop](/?entry=sample-shop) Wants to try a wine bar in Ebisu #Ebisu #winebar review:2026-10-11/21d\n" },
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
  // The basic information is open, as a person's card shows it once looked at.
  render: () => panelOf([personCalendar(), ...personas], (element) => {
    element.querySelectorAll(".basic-info").forEach((details) => { details.open = true; });
  }, { tab: "person" }),
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

// Interleaved owners make the chronological ordering visible; edits stay in the fixture API.
function scheduleRows() {
  const at = hours => new Date(Date.now() + hours * 3600000).toISOString();
  return [
    { ...personas[0], schedules: [
      { id: "dinner", at: at(30), timeZone: "Asia/Tokyo", note: "週末の夕食のお店を探す", notify: false },
      { id: "lunch", at: at(2), timeZone: "Asia/Tokyo", note: "ランチの予約を確認する", notify: true },
    ] },
    { ...person(), schedules: [
      { id: "present", at: at(5), timeZone: "Asia/Tokyo", note: "誕生日のプレゼントを選ぶ", notify: true },
    ] },
  ];
}
export const SchedulesOnly = { name: "予定だけ・日時順", render: () => panelOf(scheduleRows(), null, { tab: "schedules" }) };
export const ScheduleEdit = { name: "予定の編集・通知ON", render: () => panelOf(scheduleRows(), element => element.querySelector(".schedule-edit-button").click(), { tab: "schedules" }) };
export const ScheduleEditMuted = { name: "予定の編集・通知OFF", render: () => panelOf(scheduleRows(), element => [...element.querySelectorAll(".schedule-edit-button")].at(-1).click(), { tab: "schedules" }) };
