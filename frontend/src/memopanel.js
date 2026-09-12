// Memos and the reading log. A memo is only worth taking if it leads back to
// the passage, so every one of these is a way back into the book.
import { clear, el, relativeDate, toast } from "./dom.js";

const kindLabels = {
  "memo.created": "メモ",
  "memo.updated": "メモを書き直した",
  "memo.deleted": "メモを消した",
  "question.asked": "聞いた",
  said: "つぶやいた",
  "book.finished": "読了",
};

export function createMemoPanel({ api, reader, refresh }) {
  const body = el("div");

  function draw(opened, events) {
    clear(body);
    const memos = opened.memos || [];
    body.append(el("h3", { text: `メモ ${memos.length}` }));
    if (!memos.length) {
      body.append(el("p", { class: "hint", text: "本文を選ぶと「メモにする」が出ます。聞いているときは音声でも残せます。" }));
    }
    for (const memo of memos) {
      const text = el("div", { class: "memo-text", text: memo.text });
      body.append(
        el(
          "div",
          { class: "memo" },
          memo.quote ? el("div", { class: "quote", text: memo.quote }) : null,
          text,
          el("div", { class: "at", text: `${memo.position.absolute}文字目 ・ ${relativeDate(memo.createdAt)}` }),
          el(
            "div",
            { class: "row" },
            el("button", {
              class: "button ghost",
              text: "原文へ",
              onclick: () => reader.goTo(memo.position),
            }),
            el("button", {
              class: "button ghost",
              text: "書き直す",
              onclick: async () => {
                const next = prompt("メモ", memo.text);
                if (next === null) return;
                try {
                  await api.updateMemo(opened.book.id, memo.id, next);
                  await refresh();
                } catch (error) {
                  toast(String(error), "error");
                }
              },
            }),
            el("button", {
              class: "button ghost",
              text: "削除",
              onclick: async () => {
                if (!confirm("このメモを削除しますか")) return;
                try {
                  await api.deleteMemo(opened.book.id, memo.id);
                  await refresh();
                } catch (error) {
                  toast(String(error), "error");
                }
              },
            }),
          ),
        ),
      );
    }

    body.append(el("h3", { text: "読書の記録" }));
    if (!events.length) body.append(el("p", { class: "hint", text: "メモを残すと、日付とともにここに並びます。" }));
    let day = "";
    for (const event of events) {
      const date = relativeDate(event.at);
      if (date !== day) {
        day = date;
        body.append(el("div", { class: "timeline-day", text: date }));
      }
      body.append(
        el(
          "button",
          { class: "event source", onclick: () => reader.goTo(event.position) },
          el("span", { class: "kind", text: kindLabels[event.kind] || event.kind }),
          el("span", { class: "snippet", text: event.text || event.quote || "" }),
          el("span", { class: "at", text: `${event.position.absolute}文字目` }),
        ),
      );
    }
  }

  return { element: body, draw };
}
