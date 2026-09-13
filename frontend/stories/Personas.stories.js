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
