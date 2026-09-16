// Who is in the room.
//
// The app does one thing with a kakera: it hands it to whoever covers that
// subject. With nobody here it hands it to nobody - so this is the one state
// where the row stops being a quiet label and becomes the loudest thing on the
// screen. Everywhere else it stays a row of names, because it is also the
// answer to "who can I ask?", and asking is a tap on a name.
import { el, clear } from "./dom.js";
import { t } from "./i18n.js";
import { gloss, named } from "./builtins.js";

export function createRoom({ onOpen, onActivate, onSelect }) {
  const banner = el("section", { class: "nobody", hidden: true });
  const bar = el("div", { class: "who-bar" });
  let expanded = false;
  let rows = [];

  function drawBanner() {
    const active = rows.filter((row) => row.active);
    banner.hidden = active.length > 0;
    if (!banner.hidden) clear(banner);
    if (active.length) return;
    clear(banner).append(
      el("p", { class: "nobody-line", label: "担当が誰もいません。" }),
      el("p", { class: "hint", label: "いま放り込んだかけらは、どこにも渡りません。1人呼べば、そこから受け取ります。" }),
    );
    const quick = el("div", { class: "nobody-row" });
    for (const row of rows.filter((row) => row.builtin && !row.active)) {
      quick.append(
        el("button", {
          class: "chip add",
          title: gloss(row).domain,
          text: t("{0} を呼ぶ", `${row.icon || "◎"} ${named(row)}`),
          onclick: () => onActivate?.(row.id),
        }),
      );
    }
    if (quick.children.length) banner.append(quick);
    banner.append(
      el("div", { class: "nobody-row" },
        el("button", { class: "button primary", label: "担当をえらぶ", onclick: () => onOpen?.() })),
    );
  }

  function drawBar() {
    clear(bar);
    const active = rows.filter(row => row.active);
    for (const [index, row] of active.entries()) {
      bar.append(
        el("button", {
          class: `chip persona-chip${index >= 2 ? " persona-overflow" : ""}`,
          title: `${gloss(row).domain}\n${t("押すと担当の詳細を開きます")}`,
          text: `${row.icon || "◎"} ${row.name}`,
          onclick: () => onSelect?.(row),
        }),
      );
    }
    bar.classList.toggle("expanded", expanded);
    if (active.length > 2) {
      const more = el("button", {
        class: "chip ghost persona-more", type: "button",
        text: expanded ? t("折りたたむ") : t("その他{0}体", active.length - 2),
        "aria-expanded": String(expanded),
        onclick: () => {
          expanded = !expanded;
          bar.classList.toggle("expanded", expanded);
          more.textContent = expanded ? t("折りたたむ") : t("その他{0}体", active.length - 2);
          more.setAttribute("aria-expanded", String(expanded));
        },
      });
      bar.append(more);
    }
  }

  return {
    banner,
    bar,
    set(next) {
      rows = next || [];
      drawBanner();
      drawBar();
    },
  };
}
