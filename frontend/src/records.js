// What you have read, and what you said while reading it.
//
// Two views over the same data. The timeline is every entry you made, across
// every book, in the order you made them; the calendar is the same thing folded
// into days, with the read intervals filling in the days you read without
// writing anything down.
//
// The AI's answers are not here on purpose. Read to the same place and ask
// again and you get something much like it; what you thought at page 82 has no
// second copy.
import { clear, el, toast } from "./dom.js";

const kindLabels = {
  "memo.created": "メモ",
  "memo.updated": "メモを直した",
  "memo.deleted": "メモを消した",
  "question.asked": "聞いた",
  said: "つぶやいた",
  "book.finished": "読了",
};

export function createRecords({ api, onOpen }) {
  const view = el("div", { class: "records" });
  let tab = "timeline";

  async function draw() {
    clear(view);
    const tabs = el(
      "div",
      { class: "panel-tabs" },
      button("timeline", "Timeline"),
      button("calendar", "カレンダー"),
    );
    view.append(tabs);
    const body = el("div", { class: "records-body" });
    view.append(body);
    try {
      if (tab === "timeline") body.append(await timeline());
      else body.append(await calendar());
    } catch (error) {
      toast(String(error), "error");
    }
  }

  function button(id, label) {
    return el("button", {
      class: `button ${tab === id ? "active" : ""}`,
      text: label,
      onclick: async () => {
        tab = id;
        await draw();
      },
    });
  }

  async function timeline() {
    const entries = (await api.allTimeline()) || [];
    if (!entries.length) {
      return el("p", { class: "empty", text: "まだ記録がありません。メモを書くか、本について聞いてみてください。" });
    }
    const list = el("div", { class: "timeline" });
    for (const entry of entries) {
      const event = entry.event;
      list.append(
        el(
          "button",
          { class: "timeline-row", onclick: () => onOpen(event.bookId, event.position) },
          el("span", { class: "when", text: new Date(event.at).toLocaleString("ja-JP") }),
          el("span", { class: "book", text: entry.title }),
          el("span", { class: "kind", text: kindLabels[event.kind] || event.kind }),
          el("span", { class: "what", text: event.text || event.quote || "" }),
        ),
      );
    }
    return list;
  }

  async function calendar() {
    const days = (await api.calendar()) || [];
    if (!days.length) {
      return el("p", { class: "empty", text: "まだ読んだ記録がありません。" });
    }
    const months = new Map();
    for (const day of days) {
      const month = day.date.slice(0, 7);
      if (!months.has(month)) months.set(month, []);
      months.get(month).push(day);
    }
    const box = el("div", { class: "calendar" });
    for (const [month, list] of months) {
      const total = list.reduce((sum, day) => sum + day.chars, 0);
      box.append(
        el(
          "section",
          {},
          el(
            "h2",
            {},
            month.replace("-", "年") + "月",
            el("span", { class: "month-meta", text: `${list.length}日 ・ ${total.toLocaleString("ja-JP")}文字` }),
          ),
          grid(month, list),
        ),
      );
    }
    return box;
  }

  function grid(month, list) {
    const byDate = new Map(list.map((day) => [day.date, day]));
    const [year, monthIndex] = month.split("-").map(Number);
    const days = new Date(year, monthIndex, 0).getDate();
    const first = new Date(year, monthIndex - 1, 1).getDay();
    const most = Math.max(...list.map((day) => day.chars), 1);
    const box = el("div", { class: "calendar-grid" });
    for (const label of ["日", "月", "火", "水", "木", "金", "土"]) {
      box.append(el("span", { class: "calendar-head", text: label }));
    }
    for (let i = 0; i < first; i += 1) box.append(el("span", { class: "calendar-cell empty" }));
    for (let date = 1; date <= days; date += 1) {
      const key = `${month}-${String(date).padStart(2, "0")}`;
      const day = byDate.get(key);
      const depth = day ? 0.15 + 0.85 * (day.chars / most) : 0;
      const cell = el("span", {
        class: `calendar-cell ${day ? "read" : ""}`,
        style: day ? `--depth:${depth.toFixed(2)}` : "",
        title: day ? summaryOf(day) : "",
      }, String(date));
      if (day) {
        cell.addEventListener("click", () => show(day));
      }
      box.append(cell);
    }
    return box;
  }

  function summaryOf(day) {
    const books = day.books.map((entry) => `${entry.title} ${entry.chars.toLocaleString("ja-JP")}文字`);
    const said = day.events.length ? `・書いたこと ${day.events.length}件` : "";
    return `${books.join(" / ")}${said}`;
  }

  function show(day) {
    const backdrop = el("div", { class: "dialog-backdrop" });
    const close = () => backdrop.remove();
    backdrop.append(
      el(
        "div",
        { class: "dialog" },
        el("h3", { text: day.date }),
        el(
          "div",
          { class: "day-books" },
          day.books.map((entry) =>
            el("button", {
              class: "day-book",
              text: `${entry.title} ・ ${entry.chars.toLocaleString("ja-JP")}文字`,
              onclick: () => {
                close();
                onOpen(entry.bookId);
              },
            })
          ),
        ),
        day.events.length
          ? el(
            "div",
            { class: "timeline" },
            day.events.map((event) =>
              el(
                "button",
                { class: "timeline-row", onclick: () => { close(); onOpen(event.bookId, event.position); } },
                el("span", { class: "when", text: new Date(event.at).toLocaleTimeString("ja-JP") }),
                el("span", { class: "kind", text: kindLabels[event.kind] || event.kind }),
                el("span", { class: "what", text: event.text || event.quote || "" }),
              )
            ),
          )
          : el("p", { class: "hint", text: "この日は黙って読んでいました。" }),
        el("div", { class: "row" }, el("button", { class: "button ghost", text: "閉じる", onclick: close })),
      ),
    );
    document.body.append(backdrop);
  }

  return { element: view, draw };
}
