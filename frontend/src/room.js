// Who is in the room.
//
// The app does one thing with a kakera: it hands it to whoever covers that
// subject. With nobody here it hands it to nobody - so this is the one state
// where the row stops being a quiet label and becomes the loudest thing on the
// screen. Everywhere else it stays a row of names, because it is also the
// answer to "who can I ask?", and asking is a tap on a name.
import { el, clear } from "./dom.js";
import { t } from "./i18n.js";

export function createRoom({ onOpen, onActivate, onMention, onSpeak }) {
  const banner = el("section", { class: "nobody", hidden: true });
  const bar = el("div", { class: "who-bar" });
  const speakButton = el("button", {
    class: "chip speak",
    label: "🔈 読み上げ",
    i18nTitle: "溜まった反応を順に読み上げます",
    onclick: () => onSpeak?.(),
  });
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
          title: row.domain,
          text: t("{0} を呼ぶ", `${row.icon || "◎"} ${row.name}`),
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
    for (const row of rows.filter((row) => row.active)) {
      bar.append(
        el("button", {
          class: "chip persona-chip",
          title: `${row.domain}\n${t("押すと @{0} を書き入れます", row.name)}`,
          text: `${row.icon || "◎"} ${row.name}`,
          onclick: () => onMention?.(row),
        }),
      );
    }
    bar.append(el("span", { class: "spacer" }), speakButton);
  }

  return {
    banner,
    bar,
    set(next) {
      rows = next || [];
      drawBanner();
      drawBar();
    },
    setSpeaking(on) {
      speakButton.textContent = t(on ? "■ 停止" : "🔈 読み上げ");
      speakButton.classList.toggle("active", on);
    },
  };
}
