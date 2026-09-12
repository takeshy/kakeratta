// Choosing what kind of book this is, and who reads it with you.
//
// The category comes first, and not only because the list of companions depends
// on it. It is the choice that settles what the AI may see: a novel hides
// everything past the reader, a technical book deliberately brings in what came
// after the book was written. Nothing is sent to a model until it is made.
import { clear, el, toast } from "./dom.js";

const MAX = 3;

const categories = [
  {
    id: "novel",
    label: "小説",
    detail: "読んだところまでしか知りません。同じ質問でも、100ページ進めば答えが変わります。読み終えると振り返りが開きます。",
  },
  {
    id: "technical",
    label: "技術書",
    detail: "全体を読んだうえで答えます。本が書かれた後に変わったことも調べて伝えます。「このAPIは今は非推奨です」と言われて困る読者はいません。",
  },
];

export function createSetup({ api, onDone }) {
  const view = el("div", { class: "setup" });
  let opened = null;

  async function draw(next) {
    opened = next || opened;
    if (!opened) return;
    clear(view);
    const category = opened.book.category;
    view.append(
      el("h1", { class: "setup-title", text: opened.book.title }),
      el("p", { class: "lead", text: "この本をどう読むかを決めます。あとから変えられます。" }),
    );
    view.append(drawCategories(category));
    if (category) view.append(await drawPersonas(category));
    view.append(
      el(
        "div",
        { class: "row setup-go" },
        el("button", {
          class: "button primary",
          text: category ? "読み始める" : "先にカテゴリを選んでください",
          disabled: !category,
          onclick: async () => {
            try {
              onDone(await api.startReading(opened.book.id));
            } catch (error) {
              toast(String(error), "error");
            }
          },
        }),
      ),
    );
  }

  function drawCategories(current) {
    const box = el("div", { class: "choices" });
    for (const entry of categories) {
      box.append(
        el(
          "button",
          {
            class: `choice ${current === entry.id ? "active" : ""}`,
            onclick: async () => {
              try {
                opened = await api.setCategory(opened.book.id, entry.id);
                await draw();
              } catch (error) {
                toast(String(error), "error");
              }
            },
          },
          el("span", { class: "choice-label", text: entry.label }),
          el("span", { class: "choice-detail", text: entry.detail }),
        ),
      );
    }
    return el("section", {}, el("h2", { text: "1. この本は" }), box);
  }

  async function drawPersonas(category) {
    let plugins = [];
    try {
      plugins = (await api.personas(category)) || [];
    } catch (error) {
      toast(String(error), "error");
    }
    const chosen = opened.state.personas || [];
    const box = el("div", { class: "choices" });
    for (const plugin of plugins) {
      const inBook = chosen.includes(plugin.id);
      box.append(
        el(
          "button",
          {
            class: `choice persona ${inBook ? "active" : ""}`,
            disabled: !inBook && chosen.length >= MAX,
            onclick: async () => {
              try {
                opened = inBook
                  ? await api.removePersona(opened.book.id, plugin.id)
                  : await api.addPersona(opened.book.id, plugin.id);
                await draw();
              } catch (error) {
                toast(String(error), "error");
              }
            },
          },
          el("span", { class: "choice-label" }, el("span", { class: "avatar", text: plugin.icon || "◎" }), plugin.name),
          el("span", { class: "choice-detail", text: plugin.tone }),
          plugin.webSearch ? el("span", { class: "tag", text: "Web検索を使います" }) : null,
        ),
      );
    }
    // Reading with nobody is a proper way to read, not a fallback. It is the
    // only state in which nothing reaches a model at all.
    return el(
      "section",
      {},
      el("h2", { text: `2. 一緒に読む相手（最大${MAX}体・いなくても構いません）` }),
      box,
      el(
        "div",
        { class: "row" },
        el("button", {
          class: "button ghost",
          text: "ペルソナを追加（persona.json）",
          onclick: async () => {
            try {
              const plugin = await api.installPersonaDialog();
              if (plugin && plugin.id) toast(`${plugin.name} を追加しました`);
              await draw();
            } catch (error) {
              toast(String(error), "error");
            }
          },
        }),
        chosen.length
          ? null
          : el("span", { class: "hint", text: "「今はいない」まま読み始められます。" }),
      ),
    );
  }

  return { element: view, draw };
}
