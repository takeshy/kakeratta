// The screen, in the order it is read.
//
// Navigation sits in the header and nothing else does: the read-aloud switch is
// a mode over the timeline, so it travels with the timeline. Below the header
// comes the one thing that can stop the app working (nobody in the room), then
// the way in, then who is in the room, then the timeline - which is what the
// screen is actually for.
import { el } from "./dom.js";
import { getLanguage, t } from "./i18n.js";

// A question mark in a ring. The manual is a page of its own, so the header
// points at it rather than folding it into a panel.
const HELP_ICON = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="9.25"/><path d="M9.6 9.4a2.5 2.5 0 0 1 4.85.8c0 1.7-2.45 2.1-2.45 3.6"/><circle cx="12" cy="17" r=".6" fill="currentColor"/></svg>';

// Where the manual lives, in the reader's language. It opens in a new tab so
// the timeline, and anything half-typed in the box, stays where it was.
export function manualLink() {
  return `/manual/?lang=${getLanguage()}`;
}

export function screenNodes({ compose, room, feed, onOpen, onLogout }) {
  return [
    el("header", { class: "topbar" },
      el("span", { class: "brand" },
        el("img", { class: "brand-mark", src: "/icon.svg", alt: "", width: "22", height: "22" }),
        el("span", { class: "brand-name", text: "kakeratta" })),
      el("span", { class: "spacer" }),
      el("button", { class: "chip ghost nav", label: "検索", onclick: () => onOpen?.("search") }),
      el("button", { class: "chip ghost nav", label: "担当", onclick: () => onOpen?.("personas") }),
      el("button", { class: "chip ghost nav", label: "設定", onclick: () => onOpen?.("settings") }),
      // Only when there is an account to leave: local mode has no sign-out.
      onLogout ? el("button", { class: "chip ghost nav", label: "ログアウト", onclick: () => onLogout() }) : null,
      el("a", {
        class: "chip ghost nav help", href: manualLink(), target: "_blank", rel: "noopener",
        "aria-label": t("ヘルプ"), i18nTitle: "使い方（別タブで開く）", html: HELP_ICON,
      })),
    room.banner,
    compose,
    room.bar,
    feed,
  ];
}

// A panel over the screen. The sheet is the same whichever panel is in it, so
// the story and the app cannot disagree about what a panel looks like.
export function sheetNodes({ title, body, onClose }) {
  return el(
    "div",
    { class: "sheet" },
    el(
      "div",
      { class: "sheet-head" },
      el("strong", { label: title }),
      el("span", { class: "spacer" }),
      onClose ? el("button", { class: "chip ghost sheet-close", text: "×", "aria-label": t("閉じる"), onclick: () => onClose() }) : null,
    ),
    body,
  );
}
