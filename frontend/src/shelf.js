// The shelf is a list of current positions: which books, and where you are in
// each. That is the whole home screen.
import { clear, el, fraction, toast } from "./dom.js";

export function createShelf({ api, onOpen }) {
  const view = el("div", { class: "shelf" });

  async function draw() {
    clear(view);
    view.append(
      el("h1", { text: "書棚" }),
      el("p", { class: "lead", text: "読んだところまでを、AIも一緒に読んでいます。" }),
    );
    const actions = el(
      "div",
      { class: "shelf-actions" },
      el("button", {
        class: "button primary",
        text: "本を開く（EPUB / PDF / テキスト）",
        onclick: async () => {
          try {
            const meta = await api.importDialog();
            if (meta && meta.id) {
              toast(`${meta.title} を取り込みました`);
              await draw();
            }
          } catch (error) {
            toast(String(error), "error");
          }
        },
      }),
      el("button", { class: "button", text: "テキストを貼り付ける", onclick: paste }),
      el("button", {
        class: "button",
        text: "サンプルを読む",
        onclick: async () => {
          try {
            const meta = await api.importSample();
            await draw();
            onOpen(meta.id);
          } catch (error) {
            toast(String(error), "error");
          }
        },
      }),
    );
    view.append(actions);

    let shelf = [];
    try {
      shelf = (await api.shelf()) || [];
    } catch (error) {
      toast(String(error), "error");
    }
    if (!shelf.length) {
      view.append(el("p", { class: "empty", text: "まだ本がありません。青空文庫やProject Gutenbergのファイル、手元のEPUBやPDFを開いてください。" }));
      return;
    }
    const list = el("div", { class: "shelf-list" });
    for (const entry of shelf) {
      list.append(
        el(
          "button",
          { class: "shelf-card", onclick: () => onOpen(entry.book.id) },
          el("span", { class: "book-title", text: entry.book.title }),
          el("span", { class: "book-author", text: entry.book.author || "著者不明" }),
          el("span", { class: "bar" }, el("span", { style: `width:${fraction(entry.fraction)}` })),
          el(
            "span",
            { class: "meta" },
            el("span", { text: entry.finished ? "読了" : `${fraction(entry.fraction)} まで` }),
            el("span", { text: `メモ ${entry.memos}` }),
            el("span", { text: entry.book.sections ? `${entry.book.sections}章` : "" }),
          ),
        ),
      );
    }
    view.append(list);
  }

  function paste() {
    const title = el("input", { type: "text", placeholder: "題名" });
    const author = el("input", { type: "text", placeholder: "著者" });
    const text = el("textarea", { placeholder: "本文を貼り付けてください（青空文庫の記法にも対応します）", style: "min-height:220px" });
    const backdrop = el("div", { class: "dialog-backdrop" });
    const close = () => backdrop.remove();
    backdrop.append(
      el(
        "div",
        { class: "dialog" },
        el("h3", { text: "テキストを取り込む" }),
        title, author, text,
        el(
          "div",
          { class: "row" },
          el("button", {
            class: "button primary",
            text: "取り込む",
            onclick: async () => {
              try {
                const meta = await api.importText(title.value, author.value, text.value);
                close();
                toast(`${meta.title} を取り込みました`);
                await draw();
                onOpen(meta.id);
              } catch (error) {
                toast(String(error), "error");
              }
            },
          }),
          el("button", { class: "button ghost", text: "やめる", onclick: close }),
        ),
      ),
    );
    document.body.append(backdrop);
  }

  return { element: view, draw };
}
