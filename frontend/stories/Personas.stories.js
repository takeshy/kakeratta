import { showPersonaBuilder } from "../src/persona-builder.js";
import { getLanguage } from "../src/i18n.js";
import { createPersonas } from "../src/personas.js";
import { serve } from "./api.js";
import { sheet } from "./frame.js";
import { memory, nobody, personas, tools } from "./fixtures.js";

export default { title: "Personas", parameters: { layout: "fullscreen" } };

// The dialog the panel opens lives on <body>, outside the story root, so a
// story starts by taking down the one the previous story left.
function panelOf(rows, after) {
  for (const stale of document.querySelectorAll(".dialog-backdrop")) stale.remove();
  serve({ personas: rows, tools, memory });
  const panel = createPersonas({ onChange: () => {} });
  void panel.load().then(() => after?.(panel.element));
  return sheet("担当", panel.element);
}

export const AllPresent = { name: "4体とも有効", render: () => panelOf(personas) };

export const OneOn = {
  name: "1体だけ有効",
  render: () => panelOf(personas.map((row) => ({ ...row, active: row.id === "book" }))),
};

export const Nobody = { name: "誰もいない", render: () => panelOf(nobody) };

export const WithAdded = {
  name: "追加した担当がいる（削除できる）",
  render: () => panelOf([
    ...personas,
    { id: "wine", name: "ぶどう", icon: "🍷", model: "high", builtin: false, active: true, wakes: 2,
      domain: "ワイン。銘柄、産地、ラベルの写真、飲んだ記録。", tools: ["web_search"] },
  ]),
};

export const Memory = {
  name: "メモを見る",
  render: () => panelOf(personas, (element) => {
    [...element.querySelectorAll("button")].find((button) => /メモ|memory/i.test(button.textContent))?.click();
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
      id: "wine", name: ja ? "ぶどう" : "budou", icon: "🍷", model: "middle", tools: ["web_search"],
      domain: ja ? "飲んだワインの記録と、好みに合うワイン探し。" : "Remember wines you try and help find bottles you would enjoy.",
      tone: ja ? "気軽に話せる友達のように、短く親しみやすく。" : "Friendly and relaxed, with short replies.",
      instructions: ja ? "ワインの名前と感想を覚え、好みが見えてきたらまとめます。前の好みにつながる発見があるときに一言。" : "Remember wine names and impressions, and summarize emerging preferences. Chime in when something connects to your tastes.",
    };
    serve({ personas, tools, memory, personaDraft: {
      message: ja ? "ワイン担当「ぶどう」の案を作りました。使ってからAIで修正できます。" : "Here is budou, your wine persona. Try it first and refine it later with AI.",
      placeholder: ja ? "例：ワインに合う料理も提案してほしい。" : "Example: Suggest food pairings too.",
      draft,
    }});
    showPersonaBuilder({});
    const dialog = document.querySelector(".persona-builder");
    dialog.querySelector("textarea").value = ja
      ? "飲んだワインを覚えて、好みに合うワインを一緒に探す担当を作りたい。"
      : "A persona that remembers wines I have tried and helps me find wines I would enjoy.";
    dialog.querySelector("form").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  }),
};
